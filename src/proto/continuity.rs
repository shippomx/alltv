use std::collections::{HashMap, HashSet};

use super::TS_PACKET_SIZE;

const TIMESTAMP_MASK: u64 = (1 << 33) - 1;

#[derive(Default)]
pub struct ContinuityProcessor {
    next: HashMap<u16, u8>,
    media_cc: HashMap<u16, u8>,
    seen: HashSet<u16>,
    dropping: HashSet<u16>,
    started: bool,
    session: u64,
    marked: HashMap<u16, u64>,
    offset: u64,
    offset_ok: bool,
    last_pcr: Option<u64>,
    last_pts: HashMap<u16, u64>,
    pts_step: HashMap<u16, u64>,
    raw_pts: HashMap<u16, u64>,
    resume_pts: HashMap<u16, u64>,
    resume_max: HashMap<u16, u8>,
    video: HashMap<u16, u8>,
    wait_key: HashSet<u16>,
    params: HashMap<u16, u8>,
    wait_pmt: bool,
    wait_video: bool,
}

impl ContinuityProcessor {
    pub fn new(wait_for_pmt: bool) -> Self {
        Self {
            offset_ok: true,
            wait_pmt: wait_for_pmt,
            ..Self::default()
        }
    }

    pub fn begin_session(&mut self) {
        if self.started {
            self.session += 1;
            self.offset_ok = false;
            for pid in self.video.keys() {
                self.wait_key.insert(*pid);
                self.params.insert(*pid, 0);
            }
            self.wait_video = !self.video.is_empty();
            for (&pid, &pts) in &self.raw_pts {
                self.resume_pts.insert(pid, pts);
                self.resume_max.insert(pid, 50);
            }
            for &pid in &self.seen {
                if pid > 31 && pid != 4096 {
                    self.dropping.insert(pid);
                }
            }
        }
        self.started = true;
    }

    pub fn process(&mut self, data: &mut [u8]) -> bool {
        if data.len() % TS_PACKET_SIZE != 0 {
            return false;
        }
        for packet in data.chunks_exact_mut(TS_PACKET_SIZE) {
            if packet[0] != 0x47 {
                return false;
            }
            let pid = u16::from(packet[1] & 0x1f) << 8 | u16::from(packet[2]);
            self.read_pmt(packet, pid);
            let timeline_ready = self.normalize_timestamps(packet, pid);
            if self.session > 0
                && self.marked.get(&pid) != Some(&self.session)
                && packet[3] & 0x20 != 0
                && packet[4] > 0
            {
                packet[5] |= 0x80;
                self.marked.insert(pid, self.session);
            }
            if packet[3] & 0x10 == 0 {
                continue;
            }
            if pid <= 31 || pid == 4096 {
                rewrite_counter(packet, pid, &mut self.next);
                continue;
            }
            if self.wait_pmt {
                null_packet(packet);
                continue;
            }
            let counter = packet[3] & 15;
            let starts_pes = packet[1] & 0x40 != 0;
            if self.seen.contains(&pid) && self.media_cc.get(&pid) != Some(&counter) {
                if !starts_pes {
                    self.dropping.insert(pid);
                }
                if self.video.contains_key(&pid) {
                    self.wait_key.insert(pid);
                    self.params.insert(pid, 0);
                }
            }
            self.seen.insert(pid);
            self.media_cc.insert(pid, (counter + 1) & 15);
            if starts_pes {
                self.dropping.remove(&pid);
            }
            if !timeline_ready {
                self.dropping.insert(pid);
            }
            if self.wait_video && !self.video.contains_key(&pid) {
                self.dropping.insert(pid);
            }
            if let Some(&stream_type) = self
                .video
                .get(&pid)
                .filter(|_| self.wait_key.contains(&pid))
            {
                let (sets, random_access) = video_nals(packet, stream_type);
                let params = self.params.entry(pid).or_default();
                *params |= sets;
                if timeline_ready && random_access && *params == required_sets(stream_type) {
                    self.wait_key.remove(&pid);
                    self.dropping.remove(&pid);
                    self.wait_video = false;
                } else if timeline_ready && sets != 0 {
                    self.dropping.remove(&pid);
                } else {
                    self.dropping.insert(pid);
                }
            }
            if self.dropping.contains(&pid) {
                null_packet(packet);
                continue;
            }
            rewrite_counter(packet, pid, &mut self.next);
        }
        true
    }

    fn read_pmt(&mut self, packet: &[u8], pid: u16) {
        if pid != 4096 || packet[1] & 0x40 == 0 {
            return;
        }
        let Some(mut at) = payload_offset(packet) else {
            return;
        };
        at += usize::from(packet[at]) + 1;
        if at + 12 > packet.len() || packet[at] != 2 {
            return;
        }
        let end = (at + 3 + (usize::from(packet[at + 1] & 15) << 8) + usize::from(packet[at + 2])
            - 4)
        .min(packet.len());
        let mut pos =
            at + 12 + (usize::from(packet[at + 10] & 15) << 8) + usize::from(packet[at + 11]);
        while pos + 5 <= end {
            let kind = packet[pos];
            let elementary = u16::from(packet[pos + 1] & 31) << 8 | u16::from(packet[pos + 2]);
            if !self.seen.contains(&elementary) {
                self.dropping.insert(elementary);
            }
            if matches!(kind, 0x1b | 0x24) && self.video.insert(elementary, kind).is_none() {
                self.wait_key.insert(elementary);
                self.params.insert(elementary, 0);
                self.wait_video = true;
            }
            pos += 5 + (usize::from(packet[pos + 3] & 15) << 8) + usize::from(packet[pos + 4]);
        }
        self.wait_pmt = false;
    }

    fn normalize_timestamps(&mut self, packet: &mut [u8], pid: u16) -> bool {
        let pcr = read_pcr(packet);
        let pts = read_pts(packet);
        let resuming = self.resume_pts.contains_key(&pid);
        if let (Some((_, value)), Some(previous)) = (pts, self.resume_pts.get(&pid).copied()) {
            if !timestamp_after(value, previous) {
                let budget = self.resume_max.entry(pid).or_default();
                if *budget > 0 {
                    *budget -= 1;
                    return false;
                }
            }
            self.resume_pts.remove(&pid);
            self.resume_max.remove(&pid);
        }
        if self.resume_pts.contains_key(&pid) {
            return false;
        }
        if let Some((_, value)) = pts {
            if let Some(previous) = self.raw_pts.get(&pid).copied().filter(|_| !resuming) {
                if timestamp_after(value, previous) {
                    let step = value.wrapping_sub(previous) & TIMESTAMP_MASK;
                    if step <= 90_000 {
                        self.pts_step.insert(pid, step);
                    }
                }
            }
            self.raw_pts.insert(pid, value);
        }
        if !self.offset_ok {
            if let Some((_, value)) =
                pts.filter(|_| self.last_pts.get(&pid).copied().unwrap_or(0) != 0)
            {
                self.offset = self.last_pts[&pid]
                    .wrapping_add(self.pts_step.get(&pid).copied().unwrap_or(0).max(1))
                    .wrapping_sub(value)
                    & TIMESTAMP_MASK;
                self.offset_ok = true;
            } else if let (Some(value), Some(last)) = (pcr, self.last_pcr) {
                self.offset = last.wrapping_add(1).wrapping_sub(value) & TIMESTAMP_MASK;
                self.offset_ok = true;
            }
        }
        if !self.offset_ok {
            return true;
        }
        if let Some(value) = pcr {
            let value = (value + self.offset) & TIMESTAMP_MASK;
            write_pcr(packet, value);
            self.last_pcr = Some(value);
        }
        if let Some((at, value)) = pts {
            let value = (value + self.offset) & TIMESTAMP_MASK;
            write_timestamp(&mut packet[at..at + 5], value);
            self.last_pts.insert(pid, value);
            if packet[at] & 0xf0 == 0x30 && at + 10 <= packet.len() {
                let dts = (read_timestamp(&packet[at + 5..at + 10]) + self.offset) & TIMESTAMP_MASK;
                write_timestamp(&mut packet[at + 5..at + 10], dts);
            }
        }
        true
    }
}

fn rewrite_counter(packet: &mut [u8], pid: u16, next: &mut HashMap<u16, u8>) {
    if let Some(value) = next.get(&pid) {
        packet[3] = packet[3] & 0xf0 | value;
    }
    next.insert(pid, (packet[3] + 1) & 15);
}
fn null_packet(packet: &mut [u8]) {
    packet.fill(0xff);
    packet[..4].copy_from_slice(&[0x47, 0x1f, 0xff, 0x10]);
}
fn payload_offset(packet: &[u8]) -> Option<usize> {
    if packet[3] & 0x10 == 0 {
        return None;
    }
    let mut at = 4;
    if packet[3] & 0x20 != 0 {
        at += usize::from(packet[4]) + 1;
    }
    (at < packet.len()).then_some(at)
}
fn required_sets(kind: u8) -> u8 {
    if kind == 0x24 { 7 } else { 3 }
}
fn video_nals(packet: &[u8], kind: u8) -> (u8, bool) {
    let Some(at) = payload_offset(packet) else {
        return (0, false);
    };
    let mut sets = 0;
    let mut random = false;
    for i in at..packet.len().saturating_sub(3) {
        if packet[i..i + 3] != [0, 0, 1] {
            continue;
        }
        let h264 = packet[i + 3] & 31;
        let hevc = packet[i + 3] >> 1 & 63;
        if kind == 0x1b {
            if h264 == 7 {
                sets |= 1
            } else if h264 == 8 {
                sets |= 2
            }
            random |= h264 == 5;
        } else if kind == 0x24 {
            if (32..=34).contains(&hevc) {
                sets |= 1 << (hevc - 32);
            }
            random |= matches!(hevc, 19 | 20);
        }
    }
    (sets, random)
}
fn timestamp_after(value: u64, previous: u64) -> bool {
    let delta = value.wrapping_sub(previous) & TIMESTAMP_MASK;
    delta > 0 && delta < 1 << 32
}
fn read_pcr(packet: &[u8]) -> Option<u64> {
    if packet[3] & 0x20 == 0 || packet[4] < 7 || packet[5] & 0x10 == 0 {
        return None;
    }
    let p = &packet[6..12];
    Some(
        u64::from(p[0]) << 25
            | u64::from(p[1]) << 17
            | u64::from(p[2]) << 9
            | u64::from(p[3]) << 1
            | u64::from(p[4] >> 7),
    )
}
fn write_pcr(packet: &mut [u8], value: u64) {
    let p = &mut packet[6..12];
    p[0] = (value >> 25) as u8;
    p[1] = (value >> 17) as u8;
    p[2] = (value >> 9) as u8;
    p[3] = (value >> 1) as u8;
    p[4] = ((value & 1) as u8) << 7 | p[4] & 0x7f;
}
fn read_pts(packet: &[u8]) -> Option<(usize, u64)> {
    if packet[1] & 0x40 == 0 || packet[3] & 0x10 == 0 {
        return None;
    }
    let at = payload_offset(packet)?;
    if at + 14 > packet.len() || packet[at..at + 3] != [0, 0, 1] || packet[at + 7] & 0x80 == 0 {
        return None;
    }
    let at = at + 9;
    Some((at, read_timestamp(&packet[at..at + 5])))
}
fn read_timestamp(p: &[u8]) -> u64 {
    u64::from(p[0] >> 1 & 7) << 30
        | u64::from(p[1]) << 22
        | u64::from(p[2] >> 1) << 15
        | u64::from(p[3]) << 7
        | u64::from(p[4] >> 1)
}
fn write_timestamp(p: &mut [u8], value: u64) {
    p[0] = p[0] & 0xf0 | ((value >> 30 & 7) as u8) << 1 | 1;
    p[1] = (value >> 22) as u8;
    p[2] = ((value >> 15 & 0x7f) as u8) << 1 | 1;
    p[3] = (value >> 7) as u8;
    p[4] = ((value & 0x7f) as u8) << 1 | 1;
}

#[cfg(test)]
mod tests {
    use super::*;
    fn packet(counter: u8, start: bool) -> [u8; TS_PACKET_SIZE] {
        let mut p = [0; TS_PACKET_SIZE];
        p[..4].copy_from_slice(&[0x47, 1, 0, 0x10 | counter]);
        if start {
            p[1] |= 0x40;
        }
        p
    }
    #[test]
    fn drops_damaged_pes_until_next_start() {
        let mut data = [
            packet(1, true),
            packet(3, false),
            packet(4, false),
            packet(5, true),
        ]
        .concat();
        let mut c = ContinuityProcessor::new(false);
        assert!(c.process(&mut data));
        assert_eq!(data[TS_PACKET_SIZE + 1], 0x1f);
        assert_eq!(data[2 * TS_PACKET_SIZE + 1], 0x1f);
        assert_eq!(data[3 * TS_PACKET_SIZE + 3] & 15, 2);
    }
    #[test]
    fn rejects_non_ts_input() {
        let mut data = [0; TS_PACKET_SIZE];
        assert!(!ContinuityProcessor::new(false).process(&mut data));
    }

    #[test]
    fn rebases_pcr_and_pts_across_sessions() {
        fn timed(counter: u8, pcr: u64, pts: u64) -> [u8; TS_PACKET_SIZE] {
            let mut p = [0; TS_PACKET_SIZE];
            p[..6].copy_from_slice(&[0x47, 0x41, 0, 0x30 | counter, 7, 0x10]);
            write_pcr(&mut p, pcr);
            p[12..21].copy_from_slice(&[0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5]);
            p[21] = 0x20;
            write_timestamp(&mut p[21..26], pts);
            p
        }
        let mut continuity = ContinuityProcessor::new(false);
        continuity.begin_session();
        let mut first = timed(0, 1000, 2000);
        let mut second = timed(1, 2920, 3920);
        assert!(continuity.process(&mut first));
        assert!(continuity.process(&mut second));
        continuity.begin_session();
        let mut third = timed(2, 5000, 6000);
        assert!(continuity.process(&mut third));
        assert_eq!(read_pcr(&third), Some(4840));
        assert_eq!(read_pts(&third).map(|(_, pts)| pts), Some(5840));
    }

    fn video_packet(counter: u8, nal: u8, starts_pes: bool) -> [u8; TS_PACKET_SIZE] {
        let mut p = [0; TS_PACKET_SIZE];
        p[..4].copy_from_slice(&[0x47, 1, 0, 0x10 | counter]);
        if starts_pes {
            p[1] |= 0x40;
        }
        p[20..24].copy_from_slice(&[0, 0, 1, nal]);
        p
    }

    #[test]
    fn resumes_at_h264_idr_inside_pes() {
        let mut continuity = ContinuityProcessor::new(false);
        continuity.video.insert(256, 0x1b);
        continuity.wait_key.insert(256);
        continuity.dropping.insert(256);
        continuity.begin_session();
        continuity.params.insert(256, 3);
        let mut packet = video_packet(0, 5, false);
        assert!(continuity.process(&mut packet));
        assert_eq!(packet[1] & 0x1f, 1);
    }

    #[test]
    fn hevc_cra_does_not_resume_but_idr_does() {
        let mut continuity = ContinuityProcessor::new(false);
        continuity.video.insert(256, 0x24);
        continuity.begin_session();
        let mut initial = video_packet(0, 19 << 1, true);
        assert!(continuity.process(&mut initial));
        continuity.begin_session();
        continuity.params.insert(256, 7);
        let mut cra = video_packet(1, 21 << 1, true);
        assert!(continuity.process(&mut cra));
        assert_eq!(cra[1], 0x1f);
        let mut idr = video_packet(2, 19 << 1, true);
        assert!(continuity.process(&mut idr));
        assert_eq!(idr[1] & 0x1f, 1);
    }

    #[test]
    fn replay_suppression_stops_after_fifty_packets() {
        fn pts_packet(counter: u8, pts: u64) -> [u8; TS_PACKET_SIZE] {
            let mut p = [0; TS_PACKET_SIZE];
            p[..4].copy_from_slice(&[0x47, 0x41, 0, 0x10 | counter]);
            p[4..13].copy_from_slice(&[0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5]);
            write_timestamp(&mut p[13..18], pts);
            p
        }
        let mut continuity = ContinuityProcessor::new(false);
        continuity.begin_session();
        let mut first = pts_packet(0, 2000);
        assert!(continuity.process(&mut first));
        continuity.begin_session();
        for i in 0..50 {
            let mut replay = pts_packet((i + 1) & 15, 1900);
            assert!(continuity.process(&mut replay));
            assert_eq!(replay[1], 0x1f);
        }
        let mut bounded = pts_packet(3, 1900);
        assert!(continuity.process(&mut bounded));
        assert_eq!(bounded[1] & 0x1f, 1);
    }

    #[test]
    fn holds_audio_while_waiting_for_video_idr() {
        let mut continuity = ContinuityProcessor::new(false);
        continuity.video.insert(256, 0x1b);
        continuity.begin_session();
        continuity.params.insert(256, 3);
        let mut initial = video_packet(0, 5, true);
        assert!(continuity.process(&mut initial));
        continuity.begin_session();
        let mut audio = video_packet(0, 0, true);
        audio[2] = 1;
        assert!(continuity.process(&mut audio));
        assert_eq!(audio[1], 0x1f);
        continuity.params.insert(256, 3);
        let mut idr = video_packet(1, 5, true);
        assert!(continuity.process(&mut idr));
        let mut resumed_audio = video_packet(1, 0, true);
        resumed_audio[2] = 1;
        assert!(continuity.process(&mut resumed_audio));
        assert_eq!(resumed_audio[1] & 0x1f, 1);
    }
}
