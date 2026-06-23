#!/bin/sh
set -e

# =====================================================
# 模式一：单连接模式（最常用）
# 通过独立的环境变量配置一条隧道
# nssh 前台运行，自带断线重连
# =====================================================
if [ -n "$REMOTE_PORT" ] && [ -n "$USERNAME" ] && [ -n "$SERVER_NODE" ]; then
    CMD="-R ${REMOTE_PORT}:${LOCAL_HOST:-127.0.0.1}:${LOCAL_PORT:-8000}"
    CMD="${CMD} ${USERNAME}@${SERVER_NODE}"
    CMD="${CMD} -p ${SERVER_PORT:-20022}"
    [ -n "$PASSWORD" ] && CMD="${CMD} --passwd ${PASSWORD}"
    exec nssh ${CMD}
fi

# =====================================================
# 模式二：多连接模式
# 通过 CONNECTIONS 环境变量批量添加多条隧道
# 格式：每行一条 nssh 隧道参数（不含 --daemon）
# 示例：
#   CONNECTIONS="-R 80:127.0.0.1:8000 sh@node1.com -p 20022 --passwd p1
#                -R 81:127.0.0.1:8001 sh@node2.com -p 20022 --passwd p2"
# =====================================================
if [ -n "$CONNECTIONS" ]; then
    # 在后台启动守护进程（非 daemon-inner 模式，而是直接启动 daemon）
    # nssh --daemon 会 fork 到后台并退出父进程，所以要后台启动
    nohup nssh --daemon-inner > /tmp/nssh_daemon.log 2>&1 &
    DAEMON_PID=$!

    # 等待守护进程 Unix Socket 就绪
    sleep 2

    # 逐条添加隧道连接
    echo "$CONNECTIONS" | while IFS= read -r conn; do
        [ -z "$conn" ] && continue
        echo "[nssh] Adding tunnel: nssh --daemon ${conn}"
        nssh --daemon ${conn} || echo "[nssh] Warning: failed to add tunnel"
    done

    echo "[nssh] Daemon running (PID: ${DAEMON_PID}), all tunnels added."
    echo "[nssh] Log: /tmp/nssh_daemon.log"

    # 保持容器运行，等待守护进程
    wait ${DAEMON_PID}
    exit $?
fi

# =====================================================
# 模式三：未配置任何隧道 - 执行自定义命令
# =====================================================
echo "[nssh] No tunnel configured. Usage:"
echo "  Single tunnel: -e REMOTE_PORT=80 -e USERNAME=sh -e SERVER_NODE=host ..."
echo "  Multi tunnels: -e CONNECTIONS=\"-R 80:...\\n-R 81:...\""
echo ""
exec "$@"
