use super::playback_key::PLAYBACK_KEY;

#[derive(Debug, PartialEq, Eq)]
pub struct LoadingResponse {
    pub status: u8,
    pub code: u8,
    pub value: u32,
    pub available: bool,
    pub flags: u8,
    pub window: u16,
    pub server_time: u32,
    pub stream_id: u32,
    pub start_sequence: u32,
    pub end_sequence: u32,
}

#[derive(Debug, PartialEq, Eq)]
pub struct RangeResponse {
    pub status: u8,
    pub code: u8,
    pub value: u32,
    pub available: bool,
    pub flags: u8,
    pub window: u16,
    pub server_time: u32,
    pub stream_id: u32,
    pub start_sequence: u32,
    pub tail: u16,
    pub first_sequence: u32,
    pub end_sequence: u32,
}

pub fn decode_loading_response(wire: &[u8], channel: &[u8]) -> Option<LoadingResponse> {
    let p = decode(wire, channel)?;
    (p.len() == 50).then(|| LoadingResponse {
        status: p[24],
        code: p[25],
        value: be32(&p[26..30]),
        available: p[30] & 0x55 == 0x55,
        flags: p[31],
        window: be16(&p[32..34]),
        server_time: be32(&p[34..38]),
        stream_id: be32(&p[38..42]),
        start_sequence: be32(&p[42..46]),
        end_sequence: be32(&p[46..50]),
    })
}

pub fn decode_range_response(wire: &[u8], channel: &[u8]) -> Option<RangeResponse> {
    let p = decode(wire, channel)?;
    (p.len() >= 56).then(|| RangeResponse {
        status: p[24],
        code: p[25],
        value: be32(&p[26..30]),
        available: p[30] & 0x55 == 0x55,
        flags: p[31],
        window: be16(&p[32..34]),
        server_time: be32(&p[34..38]),
        stream_id: be32(&p[38..42]),
        start_sequence: be32(&p[42..46]),
        tail: be16(&p[46..48]),
        first_sequence: be32(&p[48..52]),
        end_sequence: be32(&p[52..56]),
    })
}

fn decode(wire: &[u8], channel: &[u8]) -> Option<Vec<u8>> {
    if wire.len() < 12 || wire[1] & 0x55 != 0x55 {
        return None;
    }
    let pivot = usize::from(wire[2] & 0x3f);
    let key_len = usize::from(wire[5]);
    if pivot < 6 || pivot >= wire.len() - 4 || key_len < 16 {
        return None;
    }
    let mut plain = Vec::with_capacity(wire.len() - 4);
    plain.extend_from_slice(&wire[..pivot]);
    plain.extend_from_slice(&wire[pivot + 4..]);
    let key_offset = usize::from(be16(&wire[3..5]) & 0x0fff);
    for i in 6..plain.len() {
        plain[i] ^= PLAYBACK_KEY[key_offset + (i - 6) % key_len];
    }
    if usize::from(be16(&plain[6..8])) != wire.len()
        || plain.len() < 24
        || (!channel.is_empty() && (channel.len() != 16 || plain[8..24] != *channel))
    {
        return None;
    }
    Some(plain)
}

fn be16(p: &[u8]) -> u16 {
    u16::from_be_bytes(p.try_into().unwrap())
}
fn be32(p: &[u8]) -> u32 {
    u32::from_be_bytes(p.try_into().unwrap())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn hex(s: &str) -> Vec<u8> {
        s.as_bytes()
            .chunks_exact(2)
            .map(|p| u8::from_str_radix(std::str::from_utf8(p).unwrap(), 16).unwrap())
            .collect()
    }

    #[test]
    fn decodes_native_range_fixture() {
        let wire = hex(
            "217dc92d83c954da330000a679670feb20e4ed9c1b5d70d630a252aa71b8ec3a2bfded6745cad7e127e52172108d538f2cecd3743fab51f879741ac70a5cfa480880dc393c4ba37822d546853b24",
        );
        let channel = hex("50a2013a0000b67c0970aaa14fd56f75");
        let r = decode_range_response(&wire, &channel).unwrap();
        assert!(r.available);
        assert_eq!((r.flags, r.window), (32, 436));
        assert_eq!(
            (r.server_time, r.stream_id, r.start_sequence),
            (1793926909, 1793926840, 0)
        );
    }
}
