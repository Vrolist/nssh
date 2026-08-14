# NSSH — AI 项目上下文

## 项目概述

nssh 是 SSH 反向隧道**客户端**（开源版），与服务端 `nwy_node_core_server_go` 配合实现内网穿透。基于标准库 `crypto/ssh` 实现 `-R` 反向隧道，支持断线重连、守护进程管理、多平台。

nssh 是旧项目 `nssh_client_go` 的开源精简版；企业定制版 `nssh-enterprise` 通过 `git subtree` 引入本仓库代码（见下方关联项目）。

## 目录结构

```
nssh/
├── main.go                  # 入口
├── base_core/               # 配置、日志、守护检测、内存监控
├── base_tunnel/             # SSH 隧道核心（tcpip-forward、channel 转发）
├── daemon/                  # 守护进程、worker 管理、传输层
├── platform/                # 平台信号处理
├── docker/                  # Docker 构建
├── .gitea/workflows/        # 本地 Gitea CI（发布上传 MinIO）
└── .github/workflows/       # GitHub CI（仅构建 + Release 附件）
```

## 双远程

| remote | 地址 | 用途 |
|---|---|---|
| `origin` | `https://github.com/Vrolist/nssh.git` | GitHub 公开仓库 |
| `gitea` | `ssh://git@192.168.2.27:1022/buladou/nssh.git` | 本地 Gitea，CI 运行于此 |

## CI 发布流程

### 标准版发布（Gitea）

推送 tag `v*` 触发 `.gitea/workflows/release-standard.yml`，matrix 构建 7 个平台（linux/darwin/freebsd × amd64/arm64/386），上传到 MinIO：

```
nssh-opensource/nssh-v{VERSION}/{label}/nssh-opensource-v{VERSION}-{label}
```

示例：`nssh-opensource/nssh-v0.17.1/darwin-amd64/nssh-opensource-v0.17.1-darwin-amd64`

- MinIO 凭据走仓库 secrets（MINIO_ENDPOINT/ACCESS_KEY/SECRET_KEY），bucket 硬编码 `nssh-opensource`
- GitHub workflow（`.github/`）仅构建并上传 GitHub Release 附件，无 MinIO 上传

### 测试

`.gitea/workflows/ci-test.yml` 在 push/PR 时运行三个模块测试（base_core/base_tunnel/daemon）。

## 版本与发布

- 标准版 tag：`v{major}.{minor}.{patch}`（如 `v0.17.1`），通过 `scripts_build/release.sh` 生成（企业仓库 nssh-enterprise 内置）
- 企业版由 nssh-enterprise 独立维护（tag `enterprise-v{YY.M.D}`），本仓库不涉及

## 最近修改记录

| 日期 | 改动 | 文件 |
|---|---|---|
| 2026-08-14 | MinIO bucket 更换为 `nssh-opensource`（与旧项目 nssh_client_go 的 bucket 隔离） | .gitea/workflows/release-standard.yml |
| 2026-08-14 | 标准版产物路径调整：`nssh-opensource/nssh-v{VERSION}/{label}/nssh-opensource-v{VERSION}-{label}` | .gitea/workflows/release-standard.yml |
| 2026-06-23 | 项目从 nssh_client_go 精简开源 | - |

## 关联项目

- **企业定制版**: `/Users/buladou/workspace/nssh-enterprise` — 通过 git subtree 引入本仓库代码，叠加企业定制（AppFlavor、设备 ID 认证、iKuai 构建矩阵）
- **旧项目**: `/Users/buladou/workspace/nssh_client_go`（已废弃，逐步下架）
- **服务端**: `/Users/buladou/workspace/nwy_node_core_server_go` — 反向隧道服务端
- **管理端**: `/Users/buladou/workspace/nssh_client_manager` — Web 管理界面
