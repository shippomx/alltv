use super::playback_key::PLAYBACK_KEY;

pub const TS_PACKET_SIZE: usize = 188;
pub const MEDIA_PAYLOAD_SIZE: usize = TS_PACKET_SIZE * 7;
const WIRE_HEADER: usize = 40;

#[derive(Debug, PartialEq, Eq)]
pub struct PlaybackDatagram {
    pub stream_id: u32,
    pub sequence: u32,
    pub payload: Vec<u8>,
}

pub fn decode_playback(wire: &[u8], channel: &[u8]) -> Option<PlaybackDatagram> {
    if wire.len() < WIRE_HEADER || wire[1] & 0x55 != 0x55 {
        return None;
    }
    let pivot = usize::from(wire[2] & 0x3f);
    let key_len = usize::from(wire[5]);
    if !(6..=35).contains(&pivot) || key_len < 16 {
        return None;
    }
    let mut header = [0; 36];
    header[..pivot].copy_from_slice(&wire[..pivot]);
    header[pivot..].copy_from_slice(&wire[pivot + 4..WIRE_HEADER]);
    let key_offset = usize::from(u16::from_be_bytes([wire[3], wire[4]]) & 0x0fff);
    for i in 0..30 {
        header[6 + i] ^= PLAYBACK_KEY[key_offset + i % key_len];
    }
    let payload_len = usize::from(u16::from_be_bytes([header[34], header[35]]));
    if usize::from(u16::from_be_bytes([header[6], header[7]])) != wire.len()
        || header[24] != 0
        || (!channel.is_empty() && (channel.len() != 16 || header[8..24] != *channel))
        || payload_len != wire.len() - WIRE_HEADER
        || payload_len % TS_PACKET_SIZE != 0
    {
        return None;
    }
    let mut payload = wire[WIRE_HEADER..].to_vec();
    if !exchange(&mut payload, header[25])
        || payload.chunks_exact(TS_PACKET_SIZE).any(|p| p[0] != 0x47)
    {
        return None;
    }
    Some(PlaybackDatagram {
        stream_id: u32::from_be_bytes(header[26..30].try_into().unwrap()),
        sequence: u32::from_be_bytes(header[30..34].try_into().unwrap()),
        payload,
    })
}

fn exchange(payload: &mut [u8], mode: u8) -> bool {
    if mode == 0 {
        return true;
    }
    let a = usize::from(mode >> 4).wrapping_sub(1);
    let b = usize::from(mode & 15).wrapping_sub(1);
    let packets = payload.len() / TS_PACKET_SIZE;
    if a >= packets || b >= packets || a == b {
        return false;
    }
    for i in 0..TS_PACKET_SIZE {
        payload.swap(a * TS_PACKET_SIZE + i, b * TS_PACKET_SIZE + i);
    }
    true
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn decodes_and_restores_packet_order() {
        let channel = *b"0123456789abcdef";
        let mut payload = vec![0; MEDIA_PAYLOAD_SIZE];
        for (i, p) in payload.chunks_exact_mut(TS_PACKET_SIZE).enumerate() {
            p[0] = 0x47;
            p[1] = i as u8;
        }
        assert!(exchange(&mut payload, 0x13));
        let mut header = [0; 36];
        header[0] = 0x23;
        header[1] = 0x55;
        header[2] = 12;
        header[3..5].copy_from_slice(&0x123_u16.to_be_bytes());
        header[5] = 16;
        header[6..8].copy_from_slice(&((WIRE_HEADER + payload.len()) as u16).to_be_bytes());
        header[8..24].copy_from_slice(&channel);
        header[25] = 0x13;
        header[26..30].copy_from_slice(&99_u32.to_be_bytes());
        header[30..34].copy_from_slice(&42_u32.to_be_bytes());
        header[34..36].copy_from_slice(&(payload.len() as u16).to_be_bytes());
        for i in 0..30 {
            header[6 + i] ^= PLAYBACK_KEY[0x123 + i % 16];
        }
        let mut wire = vec![0; WIRE_HEADER + payload.len()];
        wire[..12].copy_from_slice(&header[..12]);
        wire[16..WIRE_HEADER].copy_from_slice(&header[12..]);
        wire[WIRE_HEADER..].copy_from_slice(&payload);
        let frame = decode_playback(&wire, &channel).unwrap();
        assert_eq!((frame.stream_id, frame.sequence), (99, 42));
        for (i, p) in frame.payload.chunks_exact(TS_PACKET_SIZE).enumerate() {
            assert_eq!(p[1], i as u8);
        }
        assert!(decode_playback(&wire, b"wrong-channel-id").is_none());
    }
}
