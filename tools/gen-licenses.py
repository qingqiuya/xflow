#!/usr/bin/env python3
"""生成第三方许可清单。

    python3 tools/gen-licenses.py

产物（都在仓库根）：
  THIRD_PARTY_LICENSES.md   人能读的表格：模块 / 版本 / 许可 / 源码地址
  licenses/                 每个模块的完整许可原文（发布物里跟着走）
  installer-license.txt     NSIS 安装页用的纯文本（MIT + 第三方声明）

只统计真正被编进二进制的模块（go list -deps），不是 go.mod 里的一堆测试依赖。
"""
import os
import re
import shutil
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LIC_DIR = os.path.join(ROOT, "licenses")
LICENSE_NAMES = (
    "LICENSE", "LICENSE.txt", "LICENSE.md", "LICENSE-MIT", "LICENSE-APACHE",
    "LICENCE", "LICENCE.txt", "COPYING", "COPYING.txt", "COPYING.md",
)

# 有些模块的早期版本打包时没带 LICENSE（文件是后来才加进上游仓库的），
# go 模块缓存里就没有。这种把上游当前 LICENSE 原文放这里，脚本会优先用它。
# 文件名规则：模块路径里的非字母数字全换成 __ ，加 .txt
UPSTREAM_LIC_DIR = os.path.join(ROOT, "upstream-licenses")


def classify(text):
    t = text[:4000]
    if re.search(r"Mozilla Public License,?\s+(Version|v\.?)\s*2\.0", t, re.I) or "MPL-2.0" in t:
        return "MPL-2.0"
    if re.search(r"Apache License,?\s+(Version|v\.?)\s*2\.0", t) or "Apache License" in t:
        return "Apache-2.0"
    if "Permission is hereby granted, free of charge" in t:
        return "MIT"
    if t.startswith("ISC") or "ISC License" in t:
        return "ISC"
    if "Permission to use, copy, modify, and distribute this software for any" in t:
        return "ISC"
    if "Redistribution and use in source and binary forms" in t:
        if re.search(r"Neither the name", t):
            return "BSD-3-Clause"
        return "BSD-2-Clause"
    if "This is free and unencumbered software released into the public domain" in t:
        return "Unlicense"
    if "Do What The Fuck You Want" in t:
        return "WTFPL"
    return "UNKNOWN"


def modules():
    out = subprocess.run(
        ["go", "list", "-deps", "-f",
         "{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}", "."],
        cwd=ROOT, capture_output=True, text=True, check=True,
    ).stdout
    mods, seen = [], set()
    for line in out.splitlines():
        line = line.strip()
        if not line:
            continue
        path, ver, d = (line.split("|") + ["", "", ""])[:3]
        if path in seen:
            continue
        seen.add(path)
        mods.append((path, ver or "(local)", d))
    return mods


def main():
    mods = modules()
    if not mods:
        sys.exit("没有拿到模块列表，先在仓库根跑 go mod download")

    shutil.rmtree(LIC_DIR, ignore_errors=True)
    os.makedirs(LIC_DIR)

    rows, kinds = [], {}
    for path, ver, d in mods:
        if path == "localdl":
            continue
        files = []
        if d and os.path.isdir(d):
            for name in LICENSE_NAMES:
                p = os.path.join(d, name)
                if os.path.isfile(p):
                    files.append((name, p))
        texts = []
        for name, p in files:
            with open(p, encoding="utf-8", errors="replace") as f:
                texts.append((name, f.read()))
        source = "模块缓存"
        if not texts:
            fb = os.path.join(UPSTREAM_LIC_DIR, re.sub(r"[^A-Za-z0-9._-]+", "__", path) + ".txt")
            if os.path.isfile(fb):
                with open(fb, encoding="utf-8", errors="replace") as f:
                    texts.append((os.path.basename(fb) + "（取自上游仓库，该版本模块包内没有）", f.read()))
                source = "上游仓库"
        body = "\n\n".join(f"----- {name} -----\n{t}" for name, t in texts)
        kind = classify(body) if texts else "见备注"
        src = f"https://{path}" if "." in path.split("/")[0] else path
        rows.append((path, ver, kind, src, body, source))

        fn = re.sub(r"[^A-Za-z0-9._-]+", "__", path) + ".txt"
        with open(os.path.join(LIC_DIR, fn), "w", encoding="utf-8") as f:
            f.write(f"{path} {ver}\n许可：{kind}（来源：{source}）\n{src}\n")
            if body.strip():
                f.write("\n" + body.rstrip() + "\n")
            else:
                f.write("\n上游未提供 LICENSE 文件，以源码仓库声明为准。\n")
        kinds[kind] = kinds.get(kind, 0) + 1

    rows.sort(key=lambda r: r[0].lower())
    with open(os.path.join(ROOT, "THIRD_PARTY_LICENSES.md"), "w", encoding="utf-8") as f:
        f.write("# XFlow 第三方组件许可\n\n")
        f.write("XFlow 本体以 MIT 许可发布（见 `LICENSE`）。它静态链接了下列第三方 Go 模块，\n")
        f.write("完整许可原文在 `licenses/` 目录，随每个发布物一起分发。\n\n")
        f.write("重新生成：`python3 tools/gen-licenses.py`（依赖 `go list -deps`，反映的是实际编进二进制的模块）。\n\n")
        f.write("## 许可分布\n\n")
        for k, v in sorted(kinds.items(), key=lambda kv: -kv[1]):
            f.write(f"- {k}：{v} 个\n")
        f.write("\n## 清单\n\n")
        f.write("| 模块 | 版本 | 许可 | 源码 |\n|---|---|---|---|\n")
        for path, ver, kind, src, _, _ in rows:
            f.write(f"| `{path}` | {ver} | {kind} | {src} |\n")
        f.write("\n## 特别说明\n\n")
        f.write("### MPL-2.0（Mozilla Public License 2.0）\n\n")
        mpl = [r[0] for r in rows if r[2] == "MPL-2.0"]
        if mpl:
            f.write("下列模块以 MPL-2.0 授权，文件级 copyleft：XFlow **未修改** 这些模块的源码，\n")
            f.write("按要求在此声明其源码可从以下地址获取；如日后本地修改了其中的文件，\n")
            f.write("被修改的文件将以 MPL-2.0 公开。\n\n")
            for p in mpl:
                r = next(x for x in rows if x[0] == p)
                note = "" if r[5] == "模块缓存" else "（许可原文取自上游仓库：该版本模块包内没带 LICENSE）"
                f.write(f"- `{p}` —— https://{p}{note}\n")
        else:
            f.write("无。\n")
        f.write("\n### Apache-2.0\n\n")
        apache = [r[0] for r in rows if r[2] == "Apache-2.0"]
        f.write("这些模块的 `LICENSE` 原文在 `licenses/`；上游没有 NOTICE 文件的未额外附带。\n\n")
        for p in apache:
            f.write(f"- `{p}`\n")
        f.write("\n### 许可缺失\n\n")
        missing = [r[0] for r in rows if not r[4].strip()]
        if missing:
            for p in missing:
                f.write(f"- `{p}`：上游未提供 LICENSE 文件，以源码仓库声明为准\n")
        else:
            f.write("无。\n")

    # NSIS 安装页用：MIT 正文 + 一句话第三方声明（LicenseFile 只吃纯文本）
    mit = open(os.path.join(ROOT, "LICENSE"), encoding="utf-8").read()
    counts = "、".join(f"{k} {v} 个" for k, v in sorted(kinds.items(), key=lambda kv: -kv[1]))
    # NSIS 的 LicenseFile 只在 UTF-8 带 BOM 时才按 UTF-8 读，否则中文会乱码
    with open(os.path.join(ROOT, "installer-license.txt"), "w", encoding="utf-8-sig") as f:
        f.write("XFlow（汐流）\nCopyright (c) 2026 qingqiuya\n\n")
        f.write(mit.strip() + "\n\n")
        f.write("---\n\n本程序还包含以下开源组件：\n" + counts + "\n\n")
        f.write("完整许可原文见安装目录下的 licenses/ 目录，或 THIRD_PARTY_LICENSES.md，\n")
        f.write("也可在 https://github.com/qingqiuya/xflow 查看。\n")

    print(f"完成：{len(rows)} 个模块 · {counts}")
    print(f"  {LIC_DIR}/  ·  THIRD_PARTY_LICENSES.md  ·  installer-license.txt")


if __name__ == "__main__":
    main()
