#!/usr/bin/env python3
"""检查单镜像桌面、同源入口拒绝和进程恢复；不打开真实登录网站。"""

import base64
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid


def docker(*args, check=True, timeout=30):
    result = subprocess.run(
        ["docker", *args], capture_output=True, text=True, timeout=timeout
    )
    if check and result.returncode:
        print(result.stdout + result.stderr, file=sys.stderr)
        result.check_returncode()
    return result


def ready(url):
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1) as response:
                return response.read()
        except (OSError, urllib.error.URLError):
            time.sleep(0.2)
    raise AssertionError(f"服务未就绪：{url}")


def check_rfb(port, origin=None, expected=(401, 403), path="/desktop/websockify"):
    # 对公开端口检查未授权拒绝；容器内才执行完整 WebSocket/RFB 握手。
    with socket.create_connection(("127.0.0.1", port), timeout=10) as connection:
        key = base64.b64encode(os.urandom(16)).decode()
        origin_header = f"Origin: {origin}\r\n" if origin is not None else ""
        connection.sendall(
            (f"GET {path} HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\n"
             "Upgrade: websocket\r\nConnection: Upgrade\r\n"
             f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n"
             f"{origin_header}"
             "Sec-WebSocket-Protocol: binary\r\n\r\n").encode()
        )

        def receive(size):
            data = b""
            while len(data) < size:
                part = connection.recv(size - len(data))
                if not part:
                    raise AssertionError("桌面连接提前关闭")
                data += part
            return data

        headers = b""
        while not headers.endswith(b"\r\n\r\n"):
            headers += receive(1)
        status = int(headers.split()[1])
        assert status in expected, headers
        if status != 101:
            return
        pending = b""

        def read(size):
            nonlocal pending
            while len(pending) < size:
                first, second = receive(2)
                assert first & 15 == 2, first
                length = second & 127
                if length == 126:
                    length = struct.unpack(">H", receive(2))[0]
                elif length == 127:
                    length = struct.unpack(">Q", receive(8))[0]
                assert not second & 128
                pending += receive(length)
            value, pending = pending[:size], pending[size:]
            return value

        def send(data):
            mask = os.urandom(4)
            assert len(data) < 126
            connection.sendall(
                bytes([0x82, 0x80 | len(data)]) + mask
                + bytes(value ^ mask[index % 4] for index, value in enumerate(data))
            )

        assert read(12).startswith(b"RFB 003.")
        send(b"RFB 003.008\n")
        assert 1 in read(read(1)[0])
        send(b"\1")
        assert read(4) == b"\0" * 4
        send(b"\1")
        initial = read(24)
        assert struct.unpack(">HH", initial[:4]) == (1440, 1000)
        read(struct.unpack(">I", initial[20:24])[0])


def main(image):
    name = "xhs-desktop-test-" + uuid.uuid4().hex[:12]
    network = name + "-network"
    volume = name + "-data"
    containers = []
    try:
        metadata = json.loads(docker("image", "inspect", image).stdout)[0]
        assert set(metadata["Config"]["ExposedPorts"]) == {"18060/tcp"}
        docker("run", "--rm", "--entrypoint", "python3", image, "-c", """
from pathlib import Path
import subprocess
for name in ('libqt5webenginecore5', 'libqt5webenginewidgets5', 'fcitx5-module-cloudpinyin', 'openssh-server'):
    status = subprocess.run(['dpkg-query', '-W', '-f=${Status}', name], capture_output=True, text=True).stdout
    assert status != 'install ok installed', name
assert not list(Path('/var/lib/apt/lists').glob('*'))
assert not list(Path('/app/data').rglob('cookies.json'))
""")
        docker("network", "create", network)
        docker("volume", "create", volume)
        docker("run", "--rm", "--network", "none", "--entrypoint", "python3",
               "--mount", f"type=volume,src={volume},dst=/app/data", image, "-c", """
from pathlib import Path
import os
root = Path('/app/data')
profile = root/'config/fcitx5/profile'
profile.parent.mkdir(parents=True, exist_ok=True)
profile.write_text(Path('/usr/local/share/xhs-fcitx5-profile').read_text().replace('=rime', '=pinyin'))
for path in [root, *root.rglob('*')]:
    os.chown(path, 12345, 12345)
    path.chmod(0o755 if path.is_dir() else 0o644)
""")
        with tempfile.TemporaryDirectory(prefix="xhs-desktop-test-") as directory:
            media = Path(directory)
            containers.append(name)
            docker(
                "run", "-d", "--name", name, "--network", network,
                "--cap-drop=ALL", "--cap-add=DAC_OVERRIDE",
                "--security-opt=no-new-privileges:true", "--shm-size=1g",
                "-e", "AUTH_TOKEN=isolated-smoke-test", "-p", "127.0.0.1::18060",
                "--mount",
                f"type=bind,src={media},dst=/app/images,readonly",
                "--mount", f"type=volume,src={volume},dst=/app/data", image,
            )

            def port(container_port):
                address = docker("port", name, f"{container_port}/tcp").stdout.strip()
                assert address.startswith("127.0.0.1:"), address
                return int(address.rsplit(":", 1)[1])

            api_port = port(18060)
            bindings = json.loads(docker("inspect", name).stdout)[0]["NetworkSettings"]["Ports"]
            assert set(bindings) == {"18060/tcp"}, bindings
            docker("cp", str(Path(__file__)), name + ":/tmp/desktop_smoke.py")

            def check_ready():
                assert json.loads(ready(f"http://127.0.0.1:{port(18060)}/health"))["success"]
                assert b"<html" in ready(f"http://127.0.0.1:{port(18060)}/").lower()
                # 上游不公开：在容器内检查 noVNC 页面及真实 WebSocket/RFB。
                docker("exec", name, "python3", "-c", """
import sys
sys.path.insert(0, '/tmp')
from desktop_smoke import ready, check_rfb
assert b'noVNC' in ready('http://127.0.0.1:6080/vnc.html')
check_rfb(6080, 'http://127.0.0.1:6080', (101,), '/websockify')
check_rfb(6080, 'https://untrusted.example', (403,), '/websockify')
check_rfb(6080, None, (403,), '/websockify')
""")

            check_ready()
            for origin in (f"http://127.0.0.1:{api_port}", "https://untrusted.example", None):
                check_rfb(api_port, origin)
                headers = {"Origin": origin} if origin else {}
                request = urllib.request.Request(
                    f"http://127.0.0.1:{api_port}/desktop/vnc.html", headers=headers,
                )
                try:
                    urllib.request.urlopen(request, timeout=2)
                    raise AssertionError("未授权请求可读取桌面页面")
                except urllib.error.HTTPError as error:
                    assert error.code in (401, 403), error.code
            # 无效 JSON 在鉴权后立即失败，不访问模型或小红书。
            for token, expected in ((None, 401), ("isolated-smoke-test", 400)):
                headers = {"Content-Type": "application/json"}
                if token:
                    headers["Authorization"] = f"Bearer {token}"
                request = urllib.request.Request(
                    f"http://127.0.0.1:{api_port}/api/v1/ai/models",
                    data=b"{", headers=headers, method="POST",
                )
                try:
                    urllib.request.urlopen(request, timeout=2)
                    raise AssertionError("HTTP API 未拒绝无效请求")
                except urllib.error.HTTPError as error:
                    assert error.code == expected, error.code
                    if token:
                        assert json.load(error)["code"] == "INVALID_REQUEST"
            print("PASS 工作台首页、HTTP API 鉴权、唯一回环端口和容器内 WebSocket/RFB", flush=True)
            print("PASS 同源桌面 HTTP/WS 拒绝无 ticket 请求；上游拒绝跨站或缺失 Origin", flush=True)
            print("PASS 兼容其他 UID 的已有 data 和旧版输入法配置", flush=True)

            (media / "newest.txt").write_text("created after startup\n")
            docker("exec", name, "python3", "-c", """
import errno
from pathlib import Path
assert Path('/app/images/newest.txt').read_text() == 'created after startup\\n'
try:
    Path('/app/images/write-attempt').touch()
except OSError as error:
    assert error.errno == errno.EROFS
else:
    raise AssertionError('素材目录可写')
""")
            print("PASS 宿主新增素材可见、容器不可写", flush=True)

            docker("cp", str(Path(__file__).with_name("input_test.py")), name + ":/tmp/input_test.py")
            result = docker("exec", name, "python3", "-u", "/tmp/input_test.py", timeout=90)
            print(result.stdout, end="", flush=True)

            docker("exec", name, "python3", "-c", """
import os, signal
from pathlib import Path
pids = [int(p.name) for p in Path('/proc').iterdir() if p.name.isdigit()
        and (p/'comm').exists() and (p/'comm').read_text().strip() == 'x11vnc']
assert pids
for pid in pids:
    os.kill(pid, signal.SIGTERM)
""")
            assert int(docker("wait", name).stdout) != 0
            docker("start", name)
            check_ready()
            docker("kill", "--signal", "KILL", name)
            docker("start", name)
            check_ready()
            print("PASS 桌面故障退出、重启与 SIGKILL 后显示锁恢复", flush=True)

            for status, expected in ((0, 1), (42, 42)):
                wrapper = media / f"fcitx-exit-{status}.sh"
                wrapper.write_text(f"#!/bin/sh\nexit {status}\n")
                wrapper.chmod(0o755)
                child = name + f"-component-{status}"
                containers.append(child)
                result = docker(
                    "run", "--name", child, "--network", "none", "--mount",
                    f"type=bind,src={wrapper},dst=/usr/local/bin/fcitx5,readonly", image,
                    check=False,
                )
                assert result.returncode == expected, result.stderr
            for status in (0, 7):
                child = name + f"-app-{status}"
                containers.append(child)
                result = docker("run", "--name", child, "--network", "none", image,
                                "/bin/sh", "-c", f"exit {status}", check=False)
                assert result.returncode == status, result.stderr
            print("PASS 组件提前退出不会漏检、应用退出码保留", flush=True)
    except Exception:
        for container in containers:
            logs = docker("logs", "--tail", "30", container, check=False)
            print(logs.stdout + logs.stderr, file=sys.stderr)
        raise
    finally:
        for container in containers:
            docker("rm", "-f", container, check=False)
        docker("network", "rm", network, check=False)
        docker("volume", "rm", volume, check=False)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("用法：python3 docker/desktop/smoke_test.py <image>")
    main(sys.argv[1])
