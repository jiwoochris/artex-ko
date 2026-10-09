#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Korean-ize the last tool descriptions in server/finding_traffic.go and
server/finding_retests.go.

These are the get_finding_traffic, get_finding_retest_context and
record_finding_retest_result tools plus the binary-body message. Same rules as
the other translation scripts: only Han-containing literals, no code comments,
escapes and %d verbs preserved.

Usage:
  python3 scripts/translate-zh-finding.py [--check]
"""
import pathlib
import re
import sys

PAIRS = {
    "server/finding_traffic.go": [
        ("读取漏洞已绑定的真实流量证据，不依赖捕获开关。finding_id 使用 report_finding JSON 返回的独立漏洞记录 ID（不是第一行的探索节点 ID）。先不传 binding_id 获取清单及 version；空清单是正常情况，TCP 等非 HTTP 漏洞或未采集时仍可依据文字/命令证据编写报告，不强制绑定。有绑定时按 binding_id、side(request/response)、offset 分段读取正文。写报告时将读取的 version 作为 evidence_version 传给 update_finding_report，后者 finding_id 仍使用探索节点 ID。", "취약점에 이미 연결된 실제 트래픽 증거를 읽습니다. 캡처 스위치에 의존하지 않습니다. finding_id 는 report_finding JSON 이 반환한 독립 취약점 기록 ID 를 사용합니다(첫 줄의 탐색 노드 ID 가 아닙니다). 먼저 binding_id 없이 호출해 목록과 version 을 가져오세요. 빈 목록은 정상이며, TCP 등 비 HTTP 취약점이거나 미수집이면 텍스트/명령 증거로 보고서를 작성할 수 있으므로 연결이 강제되지 않습니다. 연결이 있으면 binding_id, side(request/response), offset 으로 본문을 분할 조회합니다. 보고서를 쓸 때 읽은 version 을 evidence_version 으로 update_finding_report 에 전달하고, 그 도구의 finding_id 는 여전히 탐색 노드 ID 를 사용합니다."),
        ("独立漏洞记录 ID", "독립 취약점 기록 ID"),
        ("清单里的绑定 ID，省略则返回清单", "목록의 바인딩 ID, 생략하면 목록을 반환"),
        ("request 或 response，默认 response", "request 또는 response, 기본 response"),
        ("[二进制正文，%d 字节；请下载查看]", "[바이너리 본문, %d 바이트; 다운로드하여 확인하세요]"),
    ],
    "server/finding_retests.go": [
        ("读取当前复测会话关联的漏洞证据快照、复测状态、补充说明与当前任务约束。无参数，只能读取本会话。", "현재 재검증 세션에 연결된 취약점 증거 스냅샷, 재검증 상태, 보충 설명, 현재 작업 제약을 읽습니다. 매개변수가 없으며 이 세션만 읽을 수 있습니다."),
        ("为当前复测会话保存唯一结论；原漏洞证据与报告保持不变。会话成功结束且结论为 fixed 时，系统自动将漏洞状态改为已修复；其他结论保留原状态。必须提供本次实际检查的证据，无法确认时写明阻塞原因。", "현재 재검증 세션에 유일한 결론을 저장합니다. 원본 취약점 증거와 보고서는 변경되지 않습니다. 세션이 성공적으로 끝나고 결론이 fixed 이면 시스템이 취약점 상태를 자동으로 수정됨으로 바꿉니다. 다른 결론은 원래 상태를 유지합니다. 이번에 실제로 확인한 증거를 반드시 제공하고, 확정할 수 없으면 차단 원인을 적으세요."),
        ("本次复测结论摘要", "이번 재검증 결론 요약"),
        ("Markdown：本次实际步骤、观察、对照、结论依据；无法确认则列出已检查内容和阻塞原因", "Markdown: 이번 실제 단계, 관찰, 대조, 결론 근거; 확정할 수 없으면 확인한 내용과 차단 원인을 나열"),
    ],
}


def main() -> int:
    check = "--check" in sys.argv
    root = pathlib.Path(__file__).resolve().parent.parent
    han = re.compile(r"[\u4e00-\u9fff]")
    applied = 0
    for rel, pairs in PAIRS.items():
        path = root / rel
        text = path.read_text(encoding="utf-8")
        before = text
        for zh, ko in sorted(pairs, key=lambda p: len(p[0]), reverse=True):
            if zh in text:
                text = text.replace(zh, ko)
                applied += 1
        if not check and text != before:
            path.write_text(text, encoding="utf-8")
    print(f"applied {applied} replacements (check={check})")

    leftover = []
    for rel in PAIRS:
        path = root / rel
        for i, line in enumerate(path.read_text(encoding="utf-8").split("\n"), 1):
            if line.strip().startswith("//"):
                continue
            for lit in re.findall(r'"((?:[^"\\]|\\.)*)"', line):
                if han.search(lit):
                    leftover.append(f"{rel}:{i}: {lit[:70]}")
    print(f"remaining Chinese literals: {len(leftover)}")
    for item in leftover[:15]:
        print("  " + item)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
