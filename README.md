# NewTV session bridge

NewTV 的纯 Go 鉴权、目录、私有直播/VOD UDP 和 HLS 转发服务。使用合法账号及 APK 证明时，主流程不启动 Android、ADB 或 NewTV App，也不伪造订阅权限或绕过 DRM。

## 运行

准备权限为 `0600` 的合法账号配置后构建并启动：

```bash
go build -o newtv-session-bridge .
./newtv-session-bridge -addr :9090 -newtv-config newtv.json
```

播放器订阅：

```text
http://<bridge-host>:9090/playlist.m3u
```

健康检查：`/healthz`；媒体输出：`/stream`。直播频道由 `provider_id` 选择；VOD 另外传入目录返回的合法播放 URL：

```bash
./newtv-session-bridge -addr :9090 \
  -newtv-config newtv.json \
  -newtv-vod 'udp://provider:port/32位内容ID'
```

启动流程会依次完成 `icAuth`、`icChlist`、FakeOption 心跳/NAT 选项、ChartMonitor Provider 解析。VOD 使用 FakeOption 返回的 `option`/`addr_adapt` 计算实际 Provider，不再依赖硬编码节点。

## 纯 Go 上游模式

已经获得直接 HTTP 媒体流地址时，可以不使用 Android、ADB 或本地播放器，直接由 Go 转发：

```bash
./newtv-session-bridge -addr :9090 -upstream 'https://media.example/stream.ts'
```

输出仍为 `http://<bridge-host>:9090/stream`，上游状态码、媒体类型、长度及 Range 响应会透传。HLS 主清单、子清单、分片、初始化分片和密钥 URI 会自动改写并通过本服务转发；内部 URL 带签名，不能把服务当作任意 URL 代理。

需要授权 Header 或 Cookie 时可重复传入 `-upstream-header`：

```bash
NEWTV_TOKEN='Bearer TOKEN' NEWTV_COOKIE='device=authorized' \
./newtv-session-bridge -addr :9090 \
  -upstream 'https://media.example/live/index.m3u8' \
  -upstream-header 'Authorization: @NEWTV_TOKEN' \
  -upstream-header 'Cookie: @NEWTV_COOKIE'
```

`@变量名` 会从环境变量读取值，避免凭据出现在进程参数中。这些 Header 只会发送给 `-upstream` 的同一主机，避免清单中的跨域分片泄露凭据。上游 `Set-Cookie` 会保存在进程内，并按标准 Cookie 域和路径规则用于后续请求；凭据不会写入磁盘。

AES-128 密钥及加密分片可以透明转发。SAMPLE-AES、Widevine、PlayReady 或 FairPlay 仍由下游授权播放器处理；本服务不绕过 DRM，也不输出解密后的媒体。

## 纯 Go 私有协议模式

完整账号模式会在启动时由 Go 执行合法鉴权、目录、ChartMonitor 和 Provider 解析，运行过程中不启动 Android、ADB 或 NewTV App：

```bash
chmod 600 newtv.json
./newtv-session-bridge -addr :9090 -newtv-config newtv.json
```

`newtv.json` 包含 `auth_endpoint`、`auth_url`、`directory_endpoint`、`chart_endpoint`、`provider_id`、`login`、APK 证明和设备架构字段。可以通过 `apk` 从合法 APK 自动计算签名证书 SHA-256 与文件 MD5，也可以直接配置 `sign` 和 `apk_md5`。Chart key/version 默认采用原生值 `key2`/`20231212`。账号配置文件必须作为凭据保护，不应提交版本库。

服务按目录下发的 `trial_minute` 提前一分钟重新执行 icAuth，并在成功后原子更新频道会话；失败时保留当前会话并每分钟重试。配置中的 `client_id` 只用于启动引导，不会阻止周期性重新鉴权。

也可以传入先前合法账号会话产生的私有协议参数：

```bash
chmod 600 session.json
./newtv-session-bridge -addr :9090 -newtv-session session.json
```

`session.json` 格式如下。三个二进制字段必须是定长十六进制；Provider 可填域名或 IP 加 UDP 端口。这里的占位值不能用于播放：

```json
{
  "channel": "00000000000000000000000000000000",
  "device_id": "00000000000000000000000000000000",
  "client_id": 1,
  "peers": ["provider.example:12345"],
  "hole_punch_peers": ["hole.example:12345"],
  "punch_payload": "510000000000000000000000000000000000000000000000000000000000",
  "vod_option": 21,
  "vod_address_adapt": 31
}
```

服务按原生时序发送三次 Range、45 块初始数据窗口和 Loading，以三块为一批请求媒体，在多个 Provider 间并发发送，按序号去重和重排，并对超时块持续重传；`/stream` 直接输出 `video/mp2t`。会话文件包含授权材料，应限制为当前用户可读且不要提交版本库。

已从运行时 PCAP 全量验证 8302 个 NewTV UDP 直播媒体帧。`internal/newtvproto.DecodePlayback` 现在按原生协议恢复被插入字节和异或处理的 36 字节逻辑头，校验总长度、频道、载荷长度与 MPEG-TS 同步字节，执行 TS 包位交换，并返回流 ID、块序号和 1316 字节标准 TS。`ReorderBuffer` 可按块序号去重和重排；本次样本中的 8302 个包对应 3148 个唯一块。

同一抓包中的 Range、Loading 和 DataRequest 均已还原。请求使用调用者传入的 16 字节频道和授权设备标识；直播使用 32 字节 nonce，VOD 使用原生的长度字节加 25 位十六进制 nonce，并执行对应的头标志、异或校验及随机插入。授权字段仅存在内存中，不会写入媒体输出。另一个 1328 字节流是 XML/EPG 数据块，不会被误判为媒体。

`internal/newtvauth` 已原生实现登录查询、账号状态 HMAC、`icChlist` 签名与请求包装、可靠 UDP Interchanger、资源 AES-128-CBC 解码及 XML 目录解析。Interchanger 支持乱序、重复包、缺块重传和尾块裁剪。它只接受合法账号及设备校验材料，不生成订阅权限或绕过 DRM。

不启动 App 获取频道目录：

```bash
chmod 600 chlist.json
go run ./cmd/chlist -config chlist.json > directory.json
```

`chlist.json` 包含 `endpoint`、`login`、`sign`（签名证书 SHA-256）、`apk_md5`、`so_md5`、`other_so`、`arch`、`os_api`。也可用 `apk` 指定一次性校验文件，由 Go 从签名证书和整个 APK 自动计算前两个摘要；显式摘要与 APK 不一致会拒绝运行。`verify` 会按原生算法由时间戳和这些摘要生成。配置 `auth_endpoint` 与 `auth_url` 后，命令会先完成合法账号认证，再获取目录；认证返回的 8 位十六进制 random 会在内存中校验，不会打印。工具不会把账号或校验材料写回磁盘。首次生成私有直播 `session.json` 所需的频道、设备和 Provider 授权字段仍必须来自服务端合法响应，不能由客户端伪造。

ChartMonitor 的 `arg` 与 `chart` 双资源也已由 Go 实现，可在合法认证后直接获取并完成资源 AES 与 gzip 解码：

```bash
chmod 600 chart.json
go run ./cmd/chart -config chart.json > chart-resources.json
```

`chart.json` 包含 `endpoint`、`host`、`name`、`product`、`key_id` 和 `version`。输出保留服务端原始 JSON 结构；服务主程序会解析其中的频道、设备 ID、Provider 与打洞节点。

VOD XML 目录和播放详情同样不需要 App，可逐级读取目录返回的 URL：

```bash
go run ./cmd/static -endpoint '<目录中的 UDP Interchanger 地址>' -target '<vod_addr 或下级 XML URL>'
```

播放详情中的 `udp://.../<内容ID>` 可直接交给 `-newtv-vod`；账号、目录和 FakeOption 仍由主程序重新合法认证。

## 纯 Go 实测验证

- 在 `tv.newtv` 进程已停止时，纯 Go VOD `/stream` 连续 30 秒输出 `5,839,092` 字节。
- 数据包含 `31,059` 个 188 字节 TS 包，`bad_sync=0`、无尾部余数。
- `ffprobe` 识别为 30 秒 MPEG-TS，H.264 视频 + AAC 音频，约 1.56 Mbit/s。
- 私有直播也已在纯 Go 模式输出并通过 MPEG-TS 检查；离线 PCAP 覆盖请求编码、媒体解码、重排和补包。

服务没有 HTTP 访问鉴权，只应绑定在可信局域网。账号配置、会话文件及 APK 证明都不应提交版本库。

## 模拟媒体输入

在没有 Android/ADB 的服务器上，可以循环输出一个 MPEG-TS 文件，完整模拟 `/playlist.m3u` 和 `/stream` 播放链路：

```bash
./newtv-session-bridge -addr :9090 \
  -input testdata/loopback-sample.ts \
  -input-bitrate 3300000
```

`testdata/loopback-sample.ts` 是本地生成的 10 秒无版权测试卡，不包含 NewTV 节目、账号或设备数据。模拟模式只用于验证服务部署、M3U 订阅和播放器兼容性；真实节目需要合法账号配置或合法会话文件。

## 旧版 Android 调试模式

未提供 `-newtv-config`、`-newtv-session`、`-upstream` 或 `-input` 时，仍保留原 Android loopback 调试模式；它不属于纯 Go 主流程。旧版 UI 目录采集命令为：

```bash
go build -o catalog ./cmd/catalog
./catalog -serial emulator-5554 -out channels.json
./newtv-session-bridge -addr :9090 -serial emulator-5554 -channels channels.json
```

本次实测采集 310 个条目，`/playlist.m3u` 会为每个条目生成唯一 `/channel/<index>` 地址。HBO HD 首次点播已成功输出 3.94 MB MPEG-TS。

旧模式只用于协议研究和回归对照；生产部署应使用 `-newtv-config` 或 `-newtv-session`。
