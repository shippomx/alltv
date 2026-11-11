use super::request::encode;
use rand::RngCore;

use super::{MEDIA_PAYLOAD_SIZE, PlaybackDatagram};

pub fn decode_vod_playback(wire: &[u8], channel: &[u8]) -> Option<PlaybackDatagram> {
    if wire.len() < 36 || wire[0] != 0x33 || wire[1] & 0x55 != 0x55 {
        return None;
    }
    let pivot = usize::from(wire[2] & 0x3f);
    let key_len = usize::from(wire[5]);
    if !(6..=31).contains(&pivot) || key_len < 16 {
        return None;
    }
    let mut plain = [0; 32];
    plain[..pivot].copy_from_slice(&wire[..pivot]);
    plain[pivot..].copy_from_slice(&wire[pivot + 4..36]);
    let key_offset = usize::from(u16::from_be_bytes([wire[3], wire[4]]) & 0x0fff);
    for (i, byte) in plain.iter_mut().enumerate().skip(6) {
        *byte ^= super::playback_key::PLAYBACK_KEY[key_offset + (i - 6) % key_len];
    }
    let payload_len = usize::from(u16::from_be_bytes([plain[30], plain[31]]));
    if usize::from(u16::from_be_bytes([plain[6], plain[7]])) != wire.len()
        || plain[24] != 0
        || payload_len != wire.len() - 36
        || payload_len != MEDIA_PAYLOAD_SIZE
        || (!channel.is_empty() && (channel.len() != 16 || plain[8..24] != *channel))
    {
        return None;
    }
    Some(PlaybackDatagram {
        stream_id: 0,
        sequence: u32::from_be_bytes(plain[26..30].try_into().unwrap()),
        payload: wire[36..].to_vec(),
    })
}

pub fn encode_vod_range_request(
    channel: [u8; 16],
    device: [u8; 16],
    client_id: u32,
    counter: u32,
    session: u16,
    fake_key: u32,
) -> Result<Vec<u8>, &'static str> {
    let mut plain = vec![0; 88];
    plain[13] = rand::random::<u8>() | 0xaa;
    plain[14..18].copy_from_slice(&counter.to_be_bytes());
    plain[18..34].copy_from_slice(&channel);
    plain[34..50].copy_from_slice(&device);
    plain[50..54].copy_from_slice(&client_id.to_be_bytes());
    plain[56..58].copy_from_slice(&session.to_be_bytes());
    plain[58..62].copy_from_slice(&fake_key.to_be_bytes());
    fill_nonce(&mut plain[62..])?;
    encode(0x31, plain, None)
}

#[allow(clippy::too_many_arguments)]
pub fn encode_vod_loading_request(
    channel: [u8; 16],
    device: [u8; 16],
    client_id: u32,
    counter: u32,
    sequence: u32,
    session: u16,
    fake_key: u32,
) -> Result<Vec<u8>, &'static str> {
    let mut plain = vec![0; 90];
    plain[13] = rand::random();
    plain[14..18].copy_from_slice(&counter.to_be_bytes());
    plain[18..34].copy_from_slice(&channel);
    plain[34..50].copy_from_slice(&device);
    plain[50..54].copy_from_slice(&client_id.to_be_bytes());
    plain[54..58].copy_from_slice(&sequence.to_be_bytes());
    plain[58..60].copy_from_slice(&session.to_be_bytes());
    plain[60..64].copy_from_slice(&fake_key.to_be_bytes());
    fill_nonce(&mut plain[64..])?;
    encode(0x32, plain, None)
}

pub fn encode_vod_data_request(
    channel: [u8; 16],
    device: [u8; 16],
    sequences: &[u32],
    session: u16,
    fake_key: u32,
) -> Result<Vec<u8>, &'static str> {
    if sequences.is_empty() || sequences.len() > 3 {
        return Err("VOD data request must contain 1-3 sequences");
    }
    let mut plain = vec![0; 79 + 4 * sequences.len()];
    plain[13..29].copy_from_slice(&channel);
    plain[29..31].copy_from_slice(&(sequences.len() as u16).to_be_bytes());
    let mut offset = 31;
    for sequence in sequences {
        plain[offset..offset + 4].copy_from_slice(&sequence.to_be_bytes());
        offset += 4;
    }
    plain[offset..offset + 16].copy_from_slice(&device);
    plain[offset + 16..offset + 18].copy_from_slice(&session.to_be_bytes());
    plain[offset + 18..offset + 22].copy_from_slice(&fake_key.to_be_bytes());
    fill_nonce(&mut plain[offset + 22..])?;
    encode(0x33, plain, None)
}

fn fill_nonce(dst: &mut [u8]) -> Result<(), &'static str> {
    if dst.len() < 2 {
        return Err("VOD nonce is too short");
    }
    let mut raw = vec![0; (dst.len() - 1).div_ceil(2)];
    rand::rng().fill_bytes(&mut raw);
    dst[0] = dst.len() as u8;
    const HEX: &[u8; 16] = b"0123456789abcdef";
    for (i, byte) in raw.into_iter().enumerate() {
        let offset = 1 + 2 * i;
        if offset < dst.len() {
            dst[offset] = HEX[usize::from(byte >> 4)];
        }
        if offset + 1 < dst.len() {
            dst[offset + 1] = HEX[usize::from(byte & 15)];
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::proto::playback_key::PLAYBACK_KEY;

    fn decode(wire: &[u8]) -> Vec<u8> {
        let pivot = usize::from(wire[2] & 0x3f);
        let key_len = usize::from(wire[5]);
        let mut plain = [&wire[..pivot], &wire[pivot + 4..]].concat();
        let offset = usize::from(u16::from_be_bytes([wire[3], wire[4]]) & 0x0fff);
        for i in 6..plain.len() {
            plain[i] ^= PLAYBACK_KEY[offset + (i - 6) % key_len];
        }
        plain
    }

    fn fixture() -> ([u8; 16], [u8; 16]) {
        let mut channel = [0; 16];
        channel.copy_from_slice(b"0123456789abcdef");
        let mut device = [0; 16];
        device[..10].copy_from_slice(b"authorized");
        (channel, device)
    }

    fn is_lower_hex(value: &[u8]) -> bool {
        !value.is_empty()
            && value
                .iter()
                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(b))
    }

    #[test]
    fn layouts_match_native_offsets() {
        let (channel, device) = fixture();
        let range = encode_vod_range_request(channel, device, 42, 3, 7, 0x12345678).unwrap();
        let p = decode(&range);
        assert_eq!((range.len(), range[0], p.len()), (92, 0x31, 88));
        assert_eq!(u32::from_be_bytes(p[50..54].try_into().unwrap()), 42);
        assert_eq!(&p[54..62], &[0, 0, 0, 7, 0x12, 0x34, 0x56, 0x78]);
        assert_eq!(p[62], 26);
        assert!(is_lower_hex(&p[63..]));

        let loading = encode_vod_loading_request(channel, device, 42, 4, 9, 7, 0x12345678).unwrap();
        let p = decode(&loading);
        assert_eq!((loading.len(), loading[0]), (94, 0x32));
        assert_eq!(&p[54..64], &[0, 0, 0, 9, 0, 7, 0x12, 0x34, 0x56, 0x78]);
        assert_eq!(p[64], 26);
        assert!(is_lower_hex(&p[65..]));

        let data = encode_vod_data_request(channel, device, &[1, 2, 3], 7, 0x12345678).unwrap();
        let p = decode(&data);
        assert_eq!((data.len(), data[0]), (95, 0x33));
        assert_eq!(u32::from_be_bytes(p[39..43].try_into().unwrap()), 3);
        assert_eq!(&p[59..65], &[0, 7, 0x12, 0x34, 0x56, 0x78]);
        assert_eq!(p[65], 26);
        assert!(is_lower_hex(&p[66..]));
    }

    #[test]
    fn data_sequence_count_is_native_bounded() {
        let (channel, device) = fixture();
        assert!(encode_vod_data_request(channel, device, &[], 0, 0).is_err());
        assert!(encode_vod_data_request(channel, device, &[1, 2, 3, 4], 0, 0).is_err());
    }

    #[test]
    fn captured_native_media_headers_decode() {
        for (text, sequence) in [
            (
                "33751a8c88673477564a6cd66da8f5526bcf23d68e2d97bef90e00001eedf159e223407e",
                48,
            ),
            (
                "33fd11681acb704896c6fdd04721fcbc6900004ce6700431dd58d049fa6bd63f095affb6",
                60,
            ),
            (
                "335f48ada6d6301b0000066a87ce3a2eed5e123e61147dbdc674f701a50b4cdc391b5bed",
                49,
            ),
        ] {
            let mut wire: Vec<u8> = text
                .as_bytes()
                .chunks_exact(2)
                .map(|x| u8::from_str_radix(std::str::from_utf8(x).unwrap(), 16).unwrap())
                .collect();
            wire.resize(36 + MEDIA_PAYLOAD_SIZE, 0);
            assert_eq!(decode_vod_playback(&wire, &[]).unwrap().sequence, sequence);
        }
    }
}
