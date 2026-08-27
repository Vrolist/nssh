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
├── debian/                  # Debian 打包（control/rules/copyright/changelog/nssh.1）
├── .gitlab-ci.yml           # Salsa CI（Debian 官方打包检查）
├── .gitea/workflows/        # 本地 Gitea CI（发布上传 MinIO）
└── .github/workflows/       # GitHub CI（仅构建 + Release 附件）
```

## 三远程

| remote | 地址 | 用途 |
|---|---|---|
| `origin` | `https://github.com/Vrolist/nssh.git` | GitHub 公开仓库 |
| `gitea` | `ssh://git@192.168.2.27:1022/buladou/nssh.git` | 本地 Gitea，CI 运行于此 |
| `salsa` | `git@salsa.debian.org:buladou/nssh.git` | Debian Salsa，跑官方打包检查（需 SSH 公钥已注册到 Salsa） |

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

## Debian 打包（进官方仓库流程）

目标是把 nssh 推进 Debian 官方仓库。当前已打通本地验证 + Salsa CI，待 sponsor 审核。

### 源码结构（关键前提）

- **单 module**：根 `go.mod` 即 `github.com/Vrolist/nssh`，子目录（base_core/base_tunnel/daemon/platform）都是子包，无独立 go.mod、无 `replace`（已合并，dh-golang 前提）
- **native 格式**：`debian/source/format` = `3.0 (native)`（源码与 debian/ 一体，无 upstream tarball）
- **Go 依赖走 Debian 包**：dh-golang 不用 GOPROXY，每个依赖对应 `golang-xxx-dev` 并声明在 `debian/control` 的 Build-Depends：
  `golang-github-rs-zerolog-dev, golang-golang-x-crypto-dev, golang-github-spf13-pflag-dev, golang-golang-x-sys-dev, golang-github-mattn-go-colorable-dev, golang-github-mattn-go-isatty-dev`

### debian/ 目录

- `control`：Build-Depends 含 `debhelper-compat (= 13), dh-golang, golang-any` + 上述 golang 依赖；`XS-Go-Import-Path: github.com/Vrolist/nssh`；Architecture: any
- `rules`：`dh $@ --buildsystem=golang --with=golang` + `override_dh_auto_build` 加 `-buildmode=pie`（否则 lintian 报 `hardening-no-pie`）
- `copyright`：只含本项目 MIT，不含 vendor 段（依赖由 Debian 包提供，不要写 vendor/ 路径，否则 lintian 报 `superfluous-file-pattern`）
- `nssh.1` + `nssh.manpages`：man page，显式声明 `debian/nssh.1` 否则 lintian 报 `no-manual-page`
- 无 `watch`（native 包不需要，有会报 `debian-watch-file-in-native-package`）
- 文件末尾必须有换行（否则 `no-newline-at-end`）
- **不要提交 `debian/files`**（dpkg-buildpackage 每次生成，已在 .gitignore）

### 本地验证（Debian 13 VM，root 执行）

VM：PVE 里 Debian13（trixie）VM，非 LXC 特权问题。工具：`debhelper dh-golang sbuild lintian pbuilder autopkgtest devscripts`。

一次性初始化：
```bash
sbuild-createchroot --include=debhelper,dh-golang,golang-any,ca-certificates \
  trixie /srv/chroot/trixie-amd64 https://mirrors.tuna.tsinghua.edu.cn/debian/
pbuilder --create --distribution trixie \
  --mirror https://mirrors.tuna.tsinghua.edu.cn/debian/ \
  --basetgz /var/cache/pbuilder/base-trixie.tgz
```

日常循环（改代码后）：
```bash
dpkg-buildpackage -S -us -uc -d      # 生成源包（-d 跳过主机依赖检查）
sbuild --arch=amd64 --dist=trixie --chroot=trixie-amd64-sbuild nssh_*.dsc  # 干净构建
grep -n 'W:\|E:' $(ls -t nssh_*.build | head -1) | tail   # 看 lintian 警告
pbuilder --build --distribution trixie --mirror https://mirrors.tuna.tsinghua.edu.cn/debian/ \
  --basetgz /var/cache/pbuilder/base-trixie.tgz nssh_*.dsc                   # 可重复构建
autopkgtest nssh_*.dsc -- schroot trixie-amd64-sbuild                        # 测试(dh-golang)
```

要点：
- 构建环境一律用**清华镜像**（VM 直连 deb.debian.org 慢了换镜像）
- sbuild chroot 实际名是 `trixie-amd64-sbuild`（不是 `trixie-amd64`，`--chroot=` 用全名或让 sbuild 自动选）
- autopkgtest 用 dh-golang 自带测试，**不需要自定义 debian/tests**（自定义 run-tests 会因 `$AUTOPKGTEST_TMP` 无源码而 FAIL，已删除）
- 改动后 VM commit + `git push gitea main`，再本地 `git fetch gitea && git merge gitea/main`，最后 `git push origin main` 同步（三处一致）

### Salsa CI

- 仓库：`salsa.debian.org/buladou/nssh`，remote 名 `salsa`
- `.gitlab-ci.yml`：include `salsa-ci.yml` + `pipeline-jobs.yml`（注意**不是**过时的 `templates/debian.yml`，那个路径不存在会 yaml invalid）
- 推送即自动跑官方 sbuild/lintian/autopkgtest/pbuilder；`SALSA_CI_DISABLE_APTLY/PIUPARTS: 1`
- Salsa push 用 SSH，需先在本机生成公钥并注册到 Salsa 账号 Preferences → SSH Keys

### 进官方后续

Salsa CI 全绿 → ITP bug 申报 → 提交 sponsor（mentors.debian.net）→ 人工审核进仓库。

## 最近修改记录

| 日期 | 改动 | 文件 |
|---|---|---|
| 2026-08-27 | Debian 打包：单 module 合并、native 格式、Build-Depends golang 依赖、PIE、man page、lintian 全绿；本地 Debian13 VM 验证 sbuild/lintian/pbuilder/autopkgtest 全过；新增 Salsa CI 并推送到 salsa | go.mod、debian/、.gitlab-ci.yml |
| 2026-08-27 | Python 包发布到 PyPI：worker 脚本 + wheel 构建，publish-python job | python/、.github/workflows/release-standard.yml |
| 2026-08-14 | MinIO bucket 更换为 `nssh-opensource`（与旧项目 nssh_client_go 的 bucket 隔离） | .gitea/workflows/release-standard.yml |
| 2026-08-14 | 标准版产物路径调整：`nssh-opensource/nssh-v{VERSION}/{label}/nssh-opensource-v{VERSION}-{label}` | .gitea/workflows/release-standard.yml |
| 2026-06-23 | 项目从 nssh_client_go 精简开源 | - |

## 关联项目

- **企业定制版**: `/Users/buladou/workspace/nssh-enterprise` — 通过 git subtree 引入本仓库代码，叠加企业定制（AppFlavor、设备 ID 认证、iKuai 构建矩阵）
- **旧项目**: `/Users/buladou/workspace/nssh_client_go`（已废弃，逐步下架）
- **服务端**: `/Users/buladou/workspace/nwy_node_core_server_go` — 反向隧道服务端
- **管理端**: `/Users/buladou/workspace/nssh_client_manager` — Web 管理界面
