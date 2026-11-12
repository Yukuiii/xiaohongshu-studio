#!/usr/bin/env python3
"""汇总唯一应用镜像的漏洞，详细发现由 CI artifact 保存。"""

import collections
import json
import os
from pathlib import Path
import sys


LEVELS = {name: rank for rank, name in enumerate(
    ("Unknown", "Negligible", "Low", "Medium", "High", "Critical")
)}


def findings(path):
    result = {}
    for match in json.loads(Path(path).read_text())["matches"]:
        vulnerability, artifact = match["vulnerability"], match["artifact"]
        key = (vulnerability["id"], artifact["name"], artifact["type"])
        rank = LEVELS[vulnerability["severity"]]
        if key not in result or rank > result[key][0]:
            result[key] = (rank, vulnerability["severity"])
    return result


def main(image_path):
    results = findings(image_path)
    counts = collections.Counter(severity for _, severity in results.values())
    severe = sorted(key for key, (rank, _) in results.items() if rank >= LEVELS["High"])
    lines = ["## 应用镜像漏洞扫描", "",
             "完整镜像统一扫描；本报告不使用另一款镜像作为基线，也不豁免已有告警。", "",
             f"- 各严重度：{dict(counts)}",
             f"- High/Critical：{len(severe)}"]
    for identifier, package, kind in severe:
        lines.append(f"  - {identifier}: {package} ({kind})")
    report = "\n".join(lines) + "\n"
    print(report)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
            summary.write(report)
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("用法：check_vulnerabilities.py <image.json>")
    sys.exit(main(sys.argv[1]))
