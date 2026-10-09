#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Translate the remaining Chinese user-facing strings in the ARTEX Korean edition.

The web UI is already localized (`web/messages/ko.json`, zero Chinese). What
still leaks Chinese into the dashboard comes from the backend: DB seeds, guard
rule descriptions, and agent prompts. This tool replaces those string literals
with Korean and leaves Chinese source comments alone.

Replacements are applied longest-first so a later, shorter entry can never
produce a half-translated literal.

Usage:
  python3 scripts/translate-zh.py           # apply
  python3 scripts/translate-zh.py --check   # report only, no writes
"""
import pathlib
import re
import sys

REPLACEMENTS = {
    "db/db.go": [
        # ---- built-in agents ----
        ("把渗透任务目标拆解成若干独立、可验证的子目标。", "침투 작업 목표를 독립적이고 검증 가능한 여러 하위 목표로 분해합니다."),
        ("目标拆解", "목표 분해"),
        ("读取态势、判定目标，只在确有未覆盖的新方向时补充探索意图（每任务一个规划循环）。", "상황을 읽고 목표를 판정하며, 아직 다루지 않은 새 방향이 있을 때만 탐색 의도를 보충합니다(작업당 계획 루프 1회)."),
        ("规划", "계획"),
        ("任务描述（测试对象/背景）", "작업 설명(테스트 대상/배경)"),
        ("测试 example.com 站点", "example.com 사이트 테스트"),
        ("任务总目标", "작업 총목표"),
        ("拿下 example.com 的管理员权限", "example.com 관리자 권한 확보"),
        ("资产计数/类型分布摘要(可选)", "자산 수/유형 분포 요약(선택)"),
        ("人机接口：观察进展，把人的意图落成 hint 或高优先级意图。", "인간-기계 인터페이스: 진행 상황을 관찰하고 사용자의 의도를 힌트 또는 고우선순위 의도로 반영합니다."),
        ("当前任务目标", "현재 작업 목표"),
        ("开局态势摘要(可选)", "초기 상황 요약(선택)"),
        ("已确认漏洞摘要(可选)", "확인된 취약점 요약(선택)"),
        ("领取一条意图执行，把发现的事实/漏洞写回知识图谱后停止。", "의도를 하나 받아 실행하고, 발견한 사실/취약점을 지식 그래프에 기록한 뒤 종료합니다."),
        ("记录代理地址(驱动 if 双文案)", "프록시 주소 기록(if 이중 문구 구동)"),
        ("worker 自我标识(可选)", "worker 자기 식별자(선택)"),
        ("平台操作助手：用工具管理任务(建/看/暂停/给提示)与资产，并可创建/修改 skill、自定义工具、MCP。", "플랫폼 운영 도우미: 도구로 작업(생성/조회/일시정지/힌트 제공)과 자산을 관리하고, skill·사용자 정의 도구·MCP를 생성/수정할 수 있습니다."),
        ("独立渗透 agent：一人从侦察→找攻击面→深入利用→验证→收尾走完整条链，自己规划、自己执行、自己对抗式验证。", "독립 침투 에이전트: 정찰→공격 표면 탐색→심층 활용→검증→마무리까지 한 사람이 전 과정을 수행하며 스스로 계획·실행·대항적 검증합니다."),
        ("渗透测试", "침투 테스트"),
        ("漏洞详细报告撰写：发现漏洞时自动触发，查取证据与执行过程后写 Markdown ", "취약점 상세 보고서 작성: 취약점 발견 시 자동 실행되며, 증거와 실행 과정을 조회한 뒤 Markdown 을 작성합니다 "),
        ("从漏洞详情手动启动，读取原证据并保存独立复测结论。", "취약점 상세에서 수동으로 시작하며, 원본 증거를 읽고 독립적인 재검증 결론을 저장합니다."),
        ("漏洞复测", "취약점 재검증"),
        ("轮询间隔（秒）", "폴링 간격(초)"),
        # ---- built-in scope presets ----
        ("[内置] 政府网站 (.gov.cn)", "[내장] 정부 웹사이트 (.gov.cn)"),
        ("[内置] 政府网站 (.gov)", "[내장] 정부 웹사이트 (.gov)"),
        ("[内置] 教育网站 (.edu.cn)", "[내장] 교육 웹사이트 (.edu.cn)"),
        ("[内置] 教育网站 (.edu)", "[내장] 교육 웹사이트 (.edu)"),
        # ---- built-in guard rules ----
        ("禁止执行递归强制删除（rm -rf / rm --recursive），可能永久损坏系统或靶机环境", "재귀 강제 삭제 실행 금지 (rm -rf / rm --recursive) — 시스템이나 대상 환경을 영구적으로 손상시킬 수 있습니다"),
        ("[内置] 递归强制删除 rm -rf", "[내장] 재귀 강제 삭제 rm -rf"),
        ("[内置] 删除系统关键目录", "[내장] 시스템 핵심 디렉터리 삭제"),
        ("禁止删除系统关键路径", "시스템 핵심 경로 삭제 금지"),
        ("禁止格式化磁盘（mkfs）", "디스크 포맷 금지 (mkfs)"),
        ("[内置] 磁盘格式化 mkfs", "[내장] 디스크 포맷 mkfs"),
        ("禁止使用 dd 覆写磁盘设备", "dd 로 디스크 장치 덮어쓰기 금지"),
        ("[内置] 覆写磁盘设备 dd", "[내장] 디스크 장치 덮어쓰기 dd"),
        ("禁止执行 Fork 炸弹", "포크 폭탄 실행 금지"),
        ("[内置] Fork 炸弹", "[내장] 포크 폭탄"),
        ("禁止执行关机或重启命令", "종료 또는 재부팅 명령 실행 금지"),
        ("[内置] 关机 / 重启", "[내장] 종료 / 재부팅"),
        ("禁止 kill -9 -1 或 killall -9（杀死所有进程）", "kill -9 -1 또는 killall -9 금지 (모든 프로세스 종료)"),
        ("[内置] 杀死全部进程", "[내장] 모든 프로세스 종료"),
        ("禁止对磁盘设备执行 shred/wipe 擦除", "디스크 장치에 shred/wipe 지우기 실행 금지"),
        ("[内置] 磁盘擦除 shred / wipe", "[내장] 디스크 지우기 shred / wipe"),
        ("禁止清空防火墙规则（iptables -F / nft flush）", "방화벽 규칙 초기화 금지 (iptables -F / nft flush)"),
        ("[内置] 清空防火墙规则", "[내장] 방화벽 규칙 초기화"),
        ("禁止执行 DROP 操作，可能不可逆地销毁数据库对象", "DROP 작업 실행 금지 — 데이터베이스 객체를 되돌릴 수 없이 파괴할 수 있습니다"),
        ("[内置] SQL DROP DATABASE / TABLE / SCHEMA", "[내장] SQL DROP DATABASE / TABLE / SCHEMA"),
        ("禁止执行 TRUNCATE，可能清空数据表所有数据", "TRUNCATE 실행 금지 — 데이터 테이블의 모든 데이터를 비울 수 있습니다"),
        ("[内置] SQL TRUNCATE", "[내장] SQL TRUNCATE"),
        ("禁止执行 MongoDB drop 操作", "MongoDB drop 작업 실행 금지"),
        ("[内置] MongoDB drop / dropDatabase", "[내장] MongoDB drop / dropDatabase"),
        ("禁止执行 Redis FLUSHALL / FLUSHDB，可能清空全部缓存数据", "Redis FLUSHALL / FLUSHDB 실행 금지 — 전체 캐시 데이터를 비울 수 있습니다"),
        ("[内置] Redis FLUSHALL / FLUSHDB", "[내장] Redis FLUSHALL / FLUSHDB"),
        ("禁止通过 curl/wget 发送 HTTP DELETE 请求，可能删除目标系统数据", "curl/wget 로 HTTP DELETE 요청 전송 금지 — 대상 시스템 데이터를 삭제할 수 있습니다"),
        ("[内置] curl / wget 发送 DELETE 请求", "[내장] curl / wget 로 DELETE 요청 전송"),
        ("禁止使用 Python HTTP 客户端发送 DELETE 请求", "Python HTTP 클라이언트로 DELETE 요청 전송 금지"),
        ("[内置] Python HTTP 客户端 DELETE（requests/httpx/aiohttp）", "[내장] Python HTTP 클라이언트 DELETE (requests/httpx/aiohttp)"),
        ("禁止在脚本中声明并发送 HTTP DELETE 请求", "스크립트에서 HTTP DELETE 요청을 선언·전송하는 것 금지"),
        ("[内置] 脚本中声明 HTTP DELETE 方法（JS/通用）", "[내장] 스크립트 내 HTTP DELETE 메서드 선언 (JS/범용)"),
        ("禁止调用批量清空或销毁类接口（/clear /wipe /flush /purge 等）", "일괄 초기화·파괴류 인터페이스 호출 금지 (/clear /wipe /flush /purge 등)"),
        ("[内置] 批量清空 / 清除接口路径", "[내장] 일괄 초기화 / 삭제 인터페이스 경로"),
        ("破坏性命令被拒绝（rm -rf / / mkfs / dd / fork bomb / 关机重启 / 覆写磁盘设备）", "파괴적 명령 거부됨 (rm -rf / / mkfs / dd / 포크 폭탄 / 종료·재부팅 / 디스크 장치 덮어쓰기)"),
        ("[内置] 破坏性系统命令", "[내장] 파괴적 시스템 명령"),
        ("疑似数据外泄管道被拒绝（命令输出经 curl/wget/nc 外传）", "데이터 유출 의심 파이프 거부됨 (명령 출력을 curl/wget/nc 로 외부 전송)"),
        ("[内置] 数据外泄管道", "[내장] 데이터 유출 파이프"),
        ("禁止调用删除类接口（/delete /remove /unlink /erase 等），不论使用哪种 HTTP 方法——多数应用的删除接口用 GET/POST 就能触发，同样会真实删除目标数据", "삭제류 인터페이스 호출 금지 (/delete /remove /unlink /erase 등) — HTTP 메서드와 무관합니다. 대부분의 애플리케이션은 삭제 인터페이스를 GET/POST 로도 트리거할 수 있고, 이 역시 대상 데이터를 실제로 삭제합니다"),
        ("[内置] 删除类接口路径", "[내장] 삭제류 인터페이스 경로"),
        # ---- built-in agent display names (quoted enums) ----
        ('"主"', '"메인"'),
        ('"执行"', '"실행"'),
    ],
}


def _ordered(pairs):
    """Longest source first, so a short entry can never split a longer one."""
    return sorted(pairs, key=lambda p: len(p[0]), reverse=True)


def main() -> int:
    check = "--check" in sys.argv
    root = pathlib.Path(__file__).resolve().parent.parent
    han = re.compile(r"[\u4e00-\u9fff]")
    changed = 0
    for rel, pairs in REPLACEMENTS.items():
        path = root / rel
        text = path.read_text(encoding="utf-8")
        for zh, ko in _ordered(pairs):
            text = text.replace(zh, ko)
        if not check:
            path.write_text(text, encoding="utf-8")
        changed += 1

    leftover = []
    for rel in REPLACEMENTS:
        text = (root / rel).read_text(encoding="utf-8")
        for i, line in enumerate(text.split("\n"), 1):
            if line.strip().startswith("//"):
                continue
            for lit in re.findall(r'"((?:[^"\\]|\\.)*)"', line):
                if han.search(lit):
                    leftover.append(f"{rel}:{i}: {lit}")
    print(f"files processed: {changed} (check={check})")
    if leftover:
        print(f"Chinese still present in string literals: {len(leftover)}")
        for item in leftover:
            print("  " + item)
        return 1
    print("no Chinese left in string literals of the processed files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
