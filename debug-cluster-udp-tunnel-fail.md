# Debug Session: cluster-udp-tunnel-fail

**Date**: 2026-10-09
**Reporter**: 用户（buladou@M1Max）
**Symptom**: 中国江西本地开发集群 8 条隧道（7 TCP + 1 UDP），TCP 正常，UDP 访问失败。
**Repro**: `echo hidou | nc -u -w 3 xydhk6.dev.buladou.home 13198` 无返回（nc -u 无响应）。
**Context**: 隧道手动连接。客户端 `nssh -R 80:192.168.2.135:9999 ... -p 7022 --passwd bcppdj` 经 QUIC 连 `cluster.dev.buladou.home:7022`。

## 链路（UDP 共享入口 / 专用双路径）

```
访客 nc -u xydhk6.dev.buladou.home:13198
  ├─ 直达节点（专用模式）: 节点 0.0.0.0:13198/udp（HandleUDPForward 绑定）
  │     → readLoop → 帧化 → udp-data@nssh channel → 客户端 udpProxy → 本地 UDP 服务
  └─ 经 L4 网关（共享入口）: TPROXY → entry socket → flow → relay → 节点 8123/udp + 8B 小头
        → SharedEntryUDPServer.handlePacket → GetTunnelByKeyHash(key_hash) → GetUDPForward()
        → injectSharedPacket → 帧化 → channel → 客户端 → 本地 UDP 服务
```

## DEBUG_EVIDENCE_DIR

- 目录: /tmp/trae-debug-cluster-udp-tunnel-fail
- NDJSON 日志: /tmp/trae-debug-cluster-udp-tunnel-fail/trae-debug-log-cluster-udp-tunnel-fail.ndjson
- env: /tmp/trae-debug-cluster-udp-tunnel-fail/cluster-udp-tunnel-fail.env

## 假设（3-5 条可证伪）

- H1: UDP 隧道未成功建立/被服务端拒绝（客户端未声明 udp-forward、端口策略拒绝、QUIC 会话被拆导致隧道反复掉线）→ 证据：节点日志 grep UDP_FORWARD / 隧道列表缺 13198
- H2: 数据面 channel 未绑定/连接断开，readLoop/injectSharedPacket 持续丢包（channel==nil / closed）→ 证据：节点日志、共享入口 metric
- H3: L4 网关 UDP 数据面拦截/转发异常：TPROXY 规则不含 13198、映射缺失、forward_protocol 非 udp/both、trusted proxy 不匹配 → 证据：网关 /status UDP stats、iptables -t mangle、数据端映射
- H4: 路由键（key_hash）不匹配：网关按 m.Key 计算、节点按端口串注册（或反之），哈希命中失败 → 证据：网关日志 drop_not_allowed / 节点 metric no_tunnel
- H5: 本地目标服务（192.168.2.135:9999/udp）不存在或不回包 → 证据：节点/客户端侧对该目标拨 UDP 测试

## 证据日志

### 采集结果（2026-10-09，节点 192.168.2.197/198/199，root/husongsxx）

**1. 隧道与映射（数据端 /api/v1/port-mapping）**
- 8 条隧道端口：14393 / 17618 / 12189 / 13198 / 11041 / 15375 / 14316 / 17403
- 13198（用户测试端口）映射：node_ip=192.168.2.199, actual_port=8122, proxy_proto=true
- 富化映射（网关实际所见）13198：`forward_protocol=both`（来自隧道配置），port_access=true → 网关 UDP 强校验可通过

**2. 三节点 UDP 数据面全部关闭**
- L4 网关配置 `/opt/nwy-server/layer4-gateway/config.yaml`：**无 `udp:` 段** → `udp.enabled=false`（默认）→ main.go 不启动 UDPForwarder → **9999/udp 无监听**
- 节点服务端 `deploy-override.yaml`：`shared_entry.enabled: true`，**无 `udp_enabled`** → `SharedEntryUDPEnabled=false`（默认）→ 节点 **8122/8123/udp 无监听**
- `ss -ulnp`：三节点均无 9999/8122/8123 UDP 监听（7922/7923 是集群 gossip，2022/2023 是 QUIC）

**3. iptables 仍截获 UDP（死端口黑洞）**
- `iptables -t mangle`：`udp dpts:10000:30000 TPROXY redirect 0.0.0.0:9999`（iptables/rules.go 无条件安装 UDP 规则）
- 访客 UDP → TPROXY → 9999/udp 无人监听 → 静默丢弃

**4. 客户端未声明 udp-forward**
- 节点 199 日志：`[SHARED_ENTRY] Tunnel ... (user: buladou1201) uses shared entry, logical port 13198 not bound`（01:00:54，TCP 路径 CreateTunnel）
- `grep 'UDP_FORWARD' nwy-node-core-info.log` = **0 条** → 13198 连接未发 `udp-forward@nssh`（默认 `--proto tcp`）
- `ss -ulnp | grep 13198` 无监听 → 服务端 HandleUDPForward 未执行（执行会绑定 13198/udp 并打 [UDP_FORWARD] 日志）

**5. 网关日志佐证**
- journalctl nwy-layer4-gateway：只打 `TPROXY 监听启动`（TCP），无 `UDP 共享入口转发器已启用`
- Oct 08 16:25 曾见 TCP 转发失败告警（旧会话映射 actual_port=14393，后重连改回共享入口 8122/8123）

## 结论（第一轮，配置层默认全关）

**根因（三节点一致）**：UDP 共享入口数据面（方案A）是灰度功能，**网关端与节点端两侧默认全关**：

| # | 环节 | 状态 | 影响 |
|---|---|---|---|
| 1 | L4 网关 `udp.enabled` | 关（配置无 udp 段） | 9999/udp 无监听，TPROXY 把业务 UDP 导到死端口 |
| 2 | 节点 `shared_entry.udp_enabled` | 关（override 无该字段） | 8122/8123/udp 无监听，网关转发也无人接 |
| 3 | 客户端 `--proto` | 默认 tcp | 13198 未声明 udp-forward → 即使 1/2 修好 GetUDPForward() 仍为 nil |

**次要发现**：deploy-override 合并（ApplyOverrideConfig）不支持 `shared_entry.udp_enabled`（config.go 1739-1752 只处理 enabled/bind_host/trusted_proxy_ips）→ 部署端无法持久下发该开关，需补代码或直接写 config.yaml。

## 结论（第二轮，代码层默认已开 + 更新二进制后仍不通）

已按用户要求把三端代码默认值改为开（不依赖配置文件）：L4 网关 `UDP.Enabled=true`、节点 `shared_entry.udp_enabled` 默认开 + `udp_forward` 默认开、客户端 `--proto` 默认 `both`。更新二进制后实测仍不通，定位到**两个新的真实阻断点**：

**根因 A（同机端口冲突，仅 dev 全栈同机场景）**
- 现象：网关（如 199，pid 14746）日志 `UDP 回程 socket bind 失败，放弃建 flow port=13198 err="...address already in use"`；`ss -ulnp` 显示同机 `*:13198/udp` 被 `nwy_node_core_s` 占用 → 网关 `drop_no_flow`，flow 无法建立，`packets_out` 恒 0。
- 链路原因：共享入口模式下 TCP 侧 `CreateTunnel` **不绑业务端口**（正确），但 UDP 侧 `HandleUDPForward` 仍 `net.ListenUDP("udp", 0.0.0.0:业务端口)` 无条件绑定（历史遗留，共享入口引入后未同步）。网关需 `bind 0.0.0.0:业务端口` 作回程 socket（回包源地址必须对齐访客发包目的地址），故同机时抢端口。
- dev 集群：客户端经 L4 负载均衡连 `cluster.dev.buladou.home:7022`，隧道随机落在 197/198/199 之一；域名 DNS 三节点轮询，访客可能命中与隧道同机的网关 → 约 1/3 概率必失败，且随重连漂移。
- 生产：网关与节点分机部署，不冲突；但节点仍无谓占用业务端口，属设计不一致。

**根因 B（可信代理校验 TCP/UDP 不对称，生产也会中）**
- 现象：节点 198 日志 `[SHARED_ENTRY_UDP] drop packet from untrusted source 192.168.2.199`（`trusted proxies: []`）。
- 原因：`isTrustedProxyIP` 空白名单 = **拒绝所有非回环来源**；而 TCP 共享入口 `isSourceAllowed` 空白名单 = **全部放行**，且 `Start()` 日志文案写 "ALL sources allowed"。同一份配置两种语义，UDP 路径直接调用 `isTrustedProxyIP`（未走 TCP 的空白名单短路）→ 网关转发的 UDP 包被全量丢弃。
- 注：TCP 7 条正常、仅 UDP 失败，正是该不对称的直接表征。

## 已实施修复（代码）

1. 节点 [shared_entry_udp.go](file:///Users/buladou/workspace/nwy_node_core_server_go/ssh/shared_entry_udp.go) `isTrustedProxyIP`：空白名单改为**全放行**，与 TCP `isSourceAllowed` / `Start()` 文案对齐（回环恒放行、nil 配置仍拒；白名单非空时行为不变）。补单测断言。
2. 节点 [udp_forward.go](file:///Users/buladou/workspace/nwy_node_core_server_go/ssh/udp_forward.go) `HandleUDPForward`：新增 `sharedMode` 判定（`GetTunnelManager(nil).useSharedEntryForTunnel(conn)`，与 TCP 同口径）。共享模式下 **socket 绑临时端口（bindPort=0）**，把业务端口让给同机网关回程 socket；`actualPort` 仍作逻辑标识（响应/channel 匹配/事件），不影响端口映射（映射用 `t.BindPort`）。日志补 `shared=/logical port=` 便于观测。

## 客户端用法（用户侧）

用户实际执行的是 `--proto uudp`（拼写错误）→ `NormalizeProto` 未知值回退 `tcp` → 该会话根本不声明 UDP。正确写法：`--proto both`（或 `--proto udp`）。另外用户本机 npm 全局版 v0.31.1（commit 6f54cbf）未含源码层默认值改动，需重装/重编译。

## 待办

- 部署新版节点二进制到 197/198/199（重启 `nwy-server@2022/@2023`）后，客户端 `--proto both` 重连，验证 `echo hidou | nc -u -w 3 xydhk6.dev.buladou.home 13198`。
- 次要隐患（本轮未改）：数据端 [mapping.go](file:///Users/buladou/workspace/nwy-data-server/internal/mapping/mapping.go) `SyncByServerID` 端口条目补全时 `ActualPort: port`（忽略共享入口端口）；节点心跳 `activeTunnelLites` 不携带 `ForwardProtocol`。当前因显式注册优先未触发，待确认。
