"""nssh - SSH reverse tunnel client.

本包不包含 Python 实现的隧道逻辑，仅作为 Go 编译的原生二进制包装器，
通过 subprocess 拉起同目录 bin/ 下的 nssh / nssh.exe 并透传参数。
"""

__version__ = "0.0.0"