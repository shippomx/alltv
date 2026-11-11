use rand::Rng;
use std::net::Ipv4Addr;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct HoleRemote {
    pub address: Ipv4Addr,
    pub port: u16,
    pub generation: u32,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Hole {
    pub slot: u8,
    pub generation: u32,
    pub remote: HoleRemote,
    pub learned_address: Ipv4Addr,
    pub learned_port: u16,
}

pub fn build_punch_packet(template: Option<&[u8; 30]>) -> [u8; 30] {
    let nonce = rand::rng().random_range(0..255);
    build_punch_packet_exact(template, nonce)
}

fn build_punch_packet_exact(template: Option<&[u8; 30]>, nonce: u8) -> [u8; 30] {
    let mut packet = template.copied().unwrap_or([0; 30]);
    packet[0] = 0x51;
    packet[1] = nonce | 0xaa;
    packet
}

pub fn build_hole_probe(slot: u8, remote: HoleRemote) -> Option<[u8; 14]> {
    if slot > 1 || remote.port == 0 || remote.generation == 0 || remote.address.is_unspecified() {
        return None;
    }
    let mut packet = [0; 14];
    packet[..4].copy_from_slice(&[0x52, 0x0e, slot, 0x52]);
    packet[4..8].copy_from_slice(&remote.address.octets());
    packet[8..10].copy_from_slice(&remote.port.to_be_bytes());
    packet[10..14].copy_from_slice(&remote.generation.to_be_bytes());
    Some(packet)
}

pub fn build_endpoint_extension(
    remote: HoleRemote,
    hole: Option<Hole>,
    tail: [u8; 25],
) -> [u8; 32] {
    let mut extension = [0; 32];
    extension[7..].copy_from_slice(&tail);
    extension[0] = if hole.is_some() { 12 } else { 6 };
    extension[1..5].copy_from_slice(&remote.address.octets());
    extension[5..7].copy_from_slice(&remote.port.to_be_bytes());
    if let Some(hole) = hole {
        extension[7..11].copy_from_slice(&hole.learned_address.octets());
        extension[11..13].copy_from_slice(&hole.learned_port.to_be_bytes());
    }
    extension
}

pub fn parse_hole_response(
    packet: &[u8],
    remotes: &[Option<HoleRemote>; 2],
    current_generations: [u32; 2],
) -> Option<Hole> {
    if packet.len() != 20 || packet[0] != 0x52 || !matches!(packet[1], 0x14 | 0x0a) || packet[2] > 1
    {
        return None;
    }
    let slot = packet[2];
    let expected = remotes[usize::from(slot)]?;
    let response_generation = u32::from_be_bytes(packet[10..14].try_into().unwrap());
    let response_remote = HoleRemote {
        address: Ipv4Addr::new(packet[14], packet[15], packet[16], packet[17]),
        port: u16::from_be_bytes([packet[18], packet[19]]),
        generation: expected.generation,
    };
    let learned_address = Ipv4Addr::new(packet[4], packet[5], packet[6], packet[7]);
    let learned_port = u16::from_be_bytes([packet[8], packet[9]]);
    if response_remote.address != expected.address
        || response_remote.port != expected.port
        || response_generation <= current_generations[usize::from(slot)]
        || learned_address.is_unspecified()
        || learned_port == 0
    {
        return None;
    }
    Some(Hole {
        slot,
        generation: response_generation,
        remote: expected,
        learned_address,
        learned_port,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn punch_preserves_native_padding() {
        let mut template = [0; 30];
        for (i, value) in template.iter_mut().enumerate().skip(2) {
            *value = i as u8;
        }
        let packet = build_punch_packet_exact(Some(&template), 0x14);
        assert_eq!(&packet[..4], &[0x51, 0xbe, 2, 3]);
        assert_eq!(&packet[2..], &template[2..]);
        assert_eq!(build_punch_packet_exact(None, 0)[2..], [0; 28]);
    }

    #[test]
    fn hole_probe_matches_native_wire() {
        let remote = HoleRemote {
            address: Ipv4Addr::new(23, 237, 231, 181),
            port: 6017,
            generation: 1,
        };
        assert_eq!(
            build_hole_probe(0, remote).unwrap(),
            [
                0x52, 0x0e, 0, 0x52, 23, 237, 231, 181, 0x17, 0x81, 0, 0, 0, 1
            ]
        );
        assert!(build_hole_probe(2, remote).is_none());
    }

    #[test]
    fn response_requires_echoed_remote_and_newer_generation() {
        let expected = HoleRemote {
            address: Ipv4Addr::new(23, 237, 231, 181),
            port: 6017,
            generation: 1,
        };
        let remotes = [Some(expected), None];
        let packet = [
            0x52, 0x14, 0, 0, 198, 51, 100, 7, 0xc3, 0x50, 0, 0, 0, 2, 23, 237, 231, 181, 0x17,
            0x81,
        ];
        let hole = parse_hole_response(&packet, &remotes, [0, 0]).unwrap();
        assert_eq!(
            (hole.learned_address, hole.learned_port),
            (Ipv4Addr::new(198, 51, 100, 7), 50000)
        );
        let mut stale = packet;
        stale[13] = 1;
        assert!(parse_hole_response(&stale, &remotes, [1, 0]).is_none());
        let mut wrong = packet;
        wrong[17] = 182;
        assert!(parse_hole_response(&wrong, &remotes, [0, 0]).is_none());
    }

    #[test]
    fn endpoint_extensions_match_native_six_and_twelve_byte_layouts() {
        let remote = HoleRemote {
            address: Ipv4Addr::new(23, 237, 231, 181),
            port: 6017,
            generation: 1,
        };
        let tail = *b"1234567890abcdef123456789";
        let simple = build_endpoint_extension(remote, None, tail);
        assert_eq!(&simple[..7], &[6, 23, 237, 231, 181, 0x17, 0x81]);
        assert_eq!(&simple[7..], &tail);
        let hole = Hole {
            slot: 0,
            generation: 2,
            remote,
            learned_address: Ipv4Addr::new(198, 51, 100, 7),
            learned_port: 50000,
        };
        let dual = build_endpoint_extension(remote, Some(hole), tail);
        assert_eq!(
            &dual[..13],
            &[
                12, 23, 237, 231, 181, 0x17, 0x81, 198, 51, 100, 7, 0xc3, 0x50
            ]
        );
        assert_eq!(&dual[13..], &tail[6..]);
    }
}
