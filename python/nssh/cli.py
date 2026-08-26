"""nssh 命令行入口：定位并拉起同目录的 Go 原生二进制。"""

import os
import subprocess
import sys


def _binary_path():
    """返回平台对应的二进制绝对路径（不存在则返回 None）。"""
    here = os.path.dirname(os.path.abspath(__file__))
    name = "nssh.exe" if os.name == "nt" else "nssh"
    path = os.path.join(here, "bin", name)
    return path if os.path.exists(path) else None


def main():
    binary = _binary_path()
    if binary is None:
        # pkg_resources 里文件路径可能指向已解压位置，二次兜底用 sys.executable 物理路径无意义，
        # 直接提示安装不完整。
        sys.stderr.write(
            f"[nssh] 缺少平台二进制: {os.path.join(os.path.dirname(os.path.abspath(__file__)), 'bin')}\n"
            "[nssh] 请通过 pip 安装对应的平台包，或检查安装是否被截断。\n"
        )
        return 1
    ret = subprocess.run([binary, *sys.argv[1:]])
    return ret.returncode if ret.returncode is not None else 1