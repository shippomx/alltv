use super::playback_key::PLAYBACK_KEY;
use rand::RngCore;
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Clone)]
pub struct DataRequest {
    pub channel: [u8; 16],
    pub stream_id: u32,
    pub sequences: Vec<u32>,
    pub device_id: [u8; 16],
    pub extension: [u8; 32],
    pub fake_key: u16,
}
#[derive(Clone)]
pub struct ControlRequest {
    pub channel: [u8; 16],
    pub device_id: [u8; 16],
    pub extension: [u8; 32],
    pub counter: u32,
    pub values: [u32; 3],
    pub decorated: bool,
    pub fake_key: u16,
}

pub fn encode_data_request(mut r: DataRequest) -> Result<Vec<u8>, &'static str> {
    if r.sequences.is_empty() || r.sequences.len() > 8180 {
        return Err("data request must contain 1-8180 sequences");
    }
    fill_nonce(&mut r.extension);
    let mut p = vec![0; 31 + r.sequences.len() * 8 + 48];
    p[13..29].copy_from_slice(&r.channel);
    p[29..31].copy_from_slice(&(r.sequences.len() as u16).to_be_bytes());
    let mut at = 31;
    for sequence in r.sequences {
        p[at..at + 4].copy_from_slice(&r.stream_id.to_be_bytes());
        p[at + 4..at + 8].copy_from_slice(&sequence.to_be_bytes());
        at += 8;
    }
    p[at..at + 16].copy_from_slice(&r.device_id);
    p[at + 16..].copy_from_slice(&r.extension);
    encode(0x23, p, Some(r.fake_key))
}
pub fn encode_range_request(r: ControlRequest) -> Result<Vec<u8>, &'static str> {
    encode_control(0x21, r)
}
pub fn encode_loading_request(r: ControlRequest) -> Result<Vec<u8>, &'static str> {
    encode_control(0x22, r)
}

fn encode_control(kind: u8, mut r: ControlRequest) -> Result<Vec<u8>, &'static str> {
    fill_nonce(&mut r.extension);
    let mut p = vec![0; 94];
    p[13] = rand::random::<u8>() | if r.decorated { 0xaa } else { 0 };
    p[14..18].copy_from_slice(&r.counter.to_be_bytes());
    p[18..34].copy_from_slice(&r.channel);
    p[34..50].copy_from_slice(&r.device_id);
    for (i, v) in r.values.into_iter().enumerate() {
        p[50 + i * 4..54 + i * 4].copy_from_slice(&v.to_be_bytes());
    }
    p[62..].copy_from_slice(&r.extension);
    encode(kind, p, Some(r.fake_key))
}
pub(crate) fn encode(kind: u8, p: Vec<u8>, key: Option<u16>) -> Result<Vec<u8>, &'static str> {
    let mut random = [0; 6];
    rand::rng().fill_bytes(&mut random);
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| "system time predates Unix epoch")?
        .as_secs() as u32;
    encode_exact(kind, p, key, now, random)
}
fn encode_exact(
    kind: u8,
    mut p: Vec<u8>,
    key: Option<u16>,
    now: u32,
    random: [u8; 6],
) -> Result<Vec<u8>, &'static str> {
    let wire_len = p.len() + 4;
    if p.len() < 13 || wire_len > u16::MAX as usize {
        return Err("request size is invalid");
    }
    let pivot = 6 + usize::from(random[0]) % 58.min(wire_len - 10);
    let key = key.unwrap_or_else(|| u16::from_be_bytes([random[1], random[2]]));
    let key_len = 16 + usize::from(random[3]) % 240;
    p[0] = kind;
    p[1] = random[4] | 0xaa;
    p[2] = pivot as u8 | ((random[5] % 255) & 3) << 6;
    p[3..5].copy_from_slice(&key.to_be_bytes());
    p[5] = key_len as u8;
    p[6..10].copy_from_slice(&now.to_be_bytes());
    p[10..12].copy_from_slice(&(wire_len as u16).to_be_bytes());
    p[12] = 0x33;
    let sum = checksum(&p);
    let offset = usize::from(key & 0x0fff);
    for i in 6..p.len() {
        p[i] ^= PLAYBACK_KEY[offset + (i - 6) % key_len];
    }
    let mut wire = Vec::with_capacity(wire_len);
    wire.extend_from_slice(&p[..pivot]);
    wire.extend_from_slice(&sum.to_be_bytes());
    wire.extend_from_slice(&p[pivot..]);
    Ok(wire)
}
fn checksum(p: &[u8]) -> u32 {
    p.chunks(4).fold(0, |sum, chunk| {
        let mut word = [0; 4];
        word[..chunk.len()].copy_from_slice(chunk);
        sum ^ u32::from_le_bytes(word)
    })
}
fn fill_nonce(value: &mut [u8; 32]) {
    if *value != [0; 32] {
        return;
    }
    let mut raw = [0; 16];
    rand::rng().fill_bytes(&mut raw);
    const HEX: &[u8; 16] = b"0123456789abcdef";
    for (i, b) in raw.into_iter().enumerate() {
        value[2 * i] = HEX[usize::from(b >> 4)];
        value[2 * i + 1] = HEX[usize::from(b & 15)];
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn decode(wire: &[u8]) -> (Vec<u8>, u32) {
        let pivot = usize::from(wire[2] & 0x3f);
        let key_len = usize::from(wire[5]);
        let sum = u32::from_be_bytes(wire[pivot..pivot + 4].try_into().unwrap());
        let mut p = [&wire[..pivot], &wire[pivot + 4..]].concat();
        let offset = usize::from(u16::from_be_bytes([wire[3], wire[4]]) & 0x0fff);
        for i in 6..p.len() {
            p[i] ^= PLAYBACK_KEY[offset + (i - 6) % key_len];
        }
        (p, sum)
    }
    #[test]
    fn deterministic_wire_matches_every_byte() {
        let mut p = vec![0; 31];
        p[13..29].copy_from_slice(b"0123456789abcdef");
        let wire =
            encode_exact(0x23, p, Some(0x11bd), 0x696aed26, [12, 0, 0, 224, 0x10, 3]).unwrap();
        let (plain, sum) = decode(&wire);
        let expected = "23bad211bdf0350c83c576b3c06744277a0858e233405151687d01f290cd6824aea568";
        let expected: Vec<u8> = expected
            .as_bytes()
            .chunks_exact(2)
            .map(|x| u8::from_str_radix(std::str::from_utf8(x).unwrap(), 16).unwrap())
            .collect();
        assert_eq!(wire, expected);
        assert_eq!(&wire[..6], &[0x23, 0xba, 0xd2, 0x11, 0xbd, 0xf0]);
        assert_eq!(&plain[6..13], &[0x69, 0x6a, 0xed, 0x26, 0, 35, 0x33]);
        assert_eq!(sum, checksum(&plain));
    }
    #[test]
    fn native_data_fixture_decodes_exactly() {
        let s = "23bae511bd69368b481e76fbc007d714733c64d1234c483953e1d92ebda56be22c19c298bd02d14d9f2c559ba80dfe4781531ed252ddae30987d04545cca331553bb0e8a56cb7c9013fd3375cf51a178a310398b71b7527232265fe2252e42928a0074e2663d3d0177560e";
        let wire: Vec<u8> = s
            .as_bytes()
            .chunks_exact(2)
            .map(|x| u8::from_str_radix(std::str::from_utf8(x).unwrap(), 16).unwrap())
            .collect();
        let (p, sum) = decode(&wire);
        assert_eq!(sum, checksum(&p));
        assert_eq!(u16::from_be_bytes(p[3..5].try_into().unwrap()), 0x11bd);
        assert_eq!(
            u32::from_be_bytes(p[31..35].try_into().unwrap()),
            1793926840
        );
        assert_eq!(u32::from_be_bytes(p[35..39].try_into().unwrap()), 0);
    }
}
