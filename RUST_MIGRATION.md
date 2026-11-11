# Rust migration

## 1. 目标复述

把当前纯 Go NewTV bridge 迁移为可独立部署的 Rust 二进制，保持 HTTP、鉴权、UDP 私有协议、M3U 和连续播放行为兼容。Go 实现保留到 Rust 端到端验收完成。

## 2. 方案设计

按稳定 seam 逐层替换：`proto` → `auth` → `runtime` → `server`。每个 Rust 模块复用现有 Go fixture 做字节级差分；不通过 FFI、子进程或 HTTP 调用 Go 来伪造完成度。

## 3. 影响面

新增根 Cargo workspace 与 `src/`。现有 Go 源码、LaunchAgent、9090 进程和凭据配置不变。

## 4. 实现摘要

- [x] 建立 Rust crate、库和二进制入口。
- [x] 迁移媒体重排缓冲与请求窗口。
- [ ] 迁移请求/响应、媒体解码和 TS 连续性。
- [ ] 迁移鉴权、Interchanger、目录、Chart 和 FakeOption。
- [ ] 迁移运行时与 HTTP/HLS 服务。
- [ ] 完成直播差分验收并切换部署。

## 5. 验证记录

每个阶段记录 `cargo test`、`cargo fmt --check`、`cargo clippy -- -D warnings` 以及对应 Go fixture 差分结果。

## 6. 遗留风险

Rust 入口在所有阶段完成前明确拒绝作为生产入口；当前线上仍运行已验证的 Go 二进制。

## 7. 回滚方式

删除 `Cargo.toml`、`Cargo.lock`、`src/` 和本文件即可回到纯 Go 仓库；生产 LaunchAgent 无需变更。
