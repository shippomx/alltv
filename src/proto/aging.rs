use rand::Rng;

use super::{LoadingResponse, RangeResponse};

pub const LIVE_SESSION_FAKE_KEY: u32 = 0x7e1ecc7f;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct AgingParam {
    pub status: u8,
    pub limit: u8,
    pub base: u16,
    pub time_delta: i32,
}

impl AgingParam {
    pub fn from_range(response: &RangeResponse, local_unix: u32) -> Option<Self> {
        Self::new(
            response.status,
            response.available,
            response.flags,
            response.window,
            response.server_time,
            local_unix,
        )
    }

    pub fn from_loading(response: &LoadingResponse, local_unix: u32) -> Option<Self> {
        Self::new(
            response.status,
            response.available,
            response.flags,
            response.window,
            response.server_time,
            local_unix,
        )
    }

    fn new(
        status: u8,
        enabled: bool,
        limit: u8,
        base: u16,
        server: u32,
        local: u32,
    ) -> Option<Self> {
        (enabled && base <= 1023).then_some(Self {
            status,
            limit,
            base,
            time_delta: server.wrapping_sub(local) as i32,
        })
    }

    pub fn stream_id(self, local_unix: u32) -> u32 {
        local_unix.wrapping_add_signed(self.time_delta)
    }
}

pub fn generate_fake_key(param: Option<AgingParam>) -> u16 {
    let mut rng = rand::rng();
    generate_fake_key_exact(param, rng.random(), rng.random())
}

fn generate_fake_key_exact(param: Option<AgingParam>, first: u32, second: u32) -> u16 {
    let high = (first % 65535) << 12;
    let value = match param {
        Some(param) => {
            let limit = if param.limit == 0 {
                255
            } else {
                u32::from(param.limit)
            };
            u32::from(param.base) + u32::from((second % limit) as u8)
        }
        None => second % 1024,
    };
    (high | value) as u16
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn maps_native_aging_fields_and_server_clock() {
        let response = RangeResponse {
            status: 3,
            code: 0,
            value: 0,
            available: true,
            flags: 32,
            window: 436,
            server_time: 1_793_926_909,
            stream_id: 0,
            start_sequence: 0,
            tail: 0,
            first_sequence: 0,
            end_sequence: 0,
        };
        let aging = AgingParam::from_range(&response, 1_793_926_900).unwrap();
        assert_eq!(
            aging,
            AgingParam {
                status: 3,
                limit: 32,
                base: 436,
                time_delta: 9
            }
        );
        assert_eq!(aging.stream_id(1_793_926_950), 1_793_926_959);
    }

    #[test]
    fn fake_key_uses_native_low_bit_formula() {
        let param = AgingParam {
            status: 0,
            limit: 32,
            base: 436,
            time_delta: 0,
        };
        let key = generate_fake_key_exact(Some(param), 0x1234, 37);
        assert_eq!(key, 0x41b9);
        assert_eq!(generate_fake_key_exact(None, 0x1234, 0x5bd), 0x41bd);
        let zero_limit = AgingParam { limit: 0, ..param };
        assert_eq!(generate_fake_key_exact(Some(zero_limit), 1, 256), 0x11b5);
    }

    #[test]
    fn rejects_disabled_or_out_of_range_aging() {
        let mut response = RangeResponse {
            status: 0,
            code: 0,
            value: 0,
            available: false,
            flags: 1,
            window: 1,
            server_time: 1,
            stream_id: 0,
            start_sequence: 0,
            tail: 0,
            first_sequence: 0,
            end_sequence: 0,
        };
        assert!(AgingParam::from_range(&response, 0).is_none());
        response.available = true;
        response.window = 1024;
        assert!(AgingParam::from_range(&response, 0).is_none());
    }
}
