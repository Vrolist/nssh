# NSSH

SSH 反向隧道客户端，用于将 NAT 或防火墙后的本地服务通过 SSH 服务器暴露到公网。

本组件提供 Python 包分发（wheel），内部包装 Go 编译的 nssh 原生二进制，不包含 Python 实现的隧道逻辑。

## 安装

```bash
pip install nssh
```

## 使用

```bash
# 密码认证
nssh -R 8080:localhost:80 user@your-server.com -p 22 --passwd your_password

# SSH 密钥认证
nssh -R 8080:localhost:80 user@your-server.com -p 22 -i ~/.ssh/id_rsa

# 守护进程模式
nssh --daemon -R 8080:localhost:80 user@your-server.com -p 22 --passwd your_password
```

更多用法见：https://github.com/Vrolist/nssh