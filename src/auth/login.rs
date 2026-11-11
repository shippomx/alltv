use hmac::{Hmac, Mac};
use sha2::Sha256;

pub const PRODUCT: &str = "newtv";
pub const VERSION: &str = "44150";
pub const BUILD: &str = "2026.10.29.1";

pub struct Login<'a> {
    pub name: &'a str,
    pub password: &'a str,
    pub android_id: &'a str,
    pub device: &'a str,
    pub cpu: &'a str,
    pub memory: &'a str,
}

impl Login<'_> {
    pub fn auth_query(&self) -> String {
        format!(
            "name={}&pass={}&androidid={}&lang=cn&ver={VERSION}&buildver={BUILD}&package=tv.newtv&devname={}&cpu={}&mem={}",
            self.name, self.password, self.android_id, self.device, self.cpu, self.memory
        )
    }
}

pub fn account_proof(name: &str, android_id: &str, timestamp: i64) -> String {
    let unsigned = format!(
        "androidid={android_id}&name={name}&product={PRODUCT}&timestamp={timestamp}&ver=Ver1000"
    );
    let mut key = format!("{name}{timestamp}");
    while key.len() < 32 {
        key.push('0');
    }
    let mut mac =
        Hmac::<Sha256>::new_from_slice(key.as_bytes()).expect("HMAC accepts any key length");
    mac.update(unsigned.as_bytes());
    let sign: String = mac
        .finalize()
        .into_bytes()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    format!("{unsigned}&sign={sign}")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn login_query_preserves_native_order_without_encoding() {
        let login = Login {
            name: "alice",
            password: "secret",
            android_id: "device-id",
            device: "TV Box",
            cpu: "8",
            memory: "4096",
        };
        assert_eq!(
            login.auth_query(),
            "name=alice&pass=secret&androidid=device-id&lang=cn&ver=44150&buildver=2026.10.29.1&package=tv.newtv&devname=TV Box&cpu=8&mem=4096"
        );
    }

    #[test]
    fn account_proof_is_stable_and_ordered() {
        assert_eq!(
            account_proof("alice", "device-id", 123),
            "androidid=device-id&name=alice&product=newtv&timestamp=123&ver=Ver1000&sign=12ce2c9d25ccb8b30585eb64be20b66c2302bf4a5c9b50878712b30af9044d82"
        );
    }
}
