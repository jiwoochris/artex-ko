#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Korean-ize the orchestration tool descriptions and the reporter prompt in
server/orchestration.go.

These are the only remaining Chinese tool strings: the orchestration handlers
(list_tasks, spawn_task, pause_task, get_task_graph, the *_task_worker_trace
family, add_task_hint, update_finding_report) are defined here rather than in
agent/tools*.go, and the reporter trigger prompt (seeded into the DB) is here
too.

Only string literals with Han characters are replaced; Chinese comments and the
`%d`/`\\n` escapes are preserved exactly.

Usage:
  python3 scripts/translate-zh-orchestration.py [--check]
"""
import pathlib
import re
import sys

PAIRS = [
    ("列出所有任务(id/描述/目标/状态/运行时长/父任务/LLM 配置)，编排 agent 用它掌握全局、看哪些任务卡太久、各自用哪个 LLM。运行时长：运行中=创建→现在，终态=创建→最后活动(秒)。llm_profile：任务 planner/worker 用的配置名，(激活配置)=跟随全局激活。", "모든 작업(id/설명/목표/상태/실행 시간/부모 작업/LLM 설정)을 나열합니다. 오케스트레이션 agent 가 전역을 파악하고 어떤 작업이 너무 오래 걸리는지, 각자 어떤 LLM 을 쓰는지 보는 데 사용합니다. 실행 시간: 실행 중=생성→현재, 종료 상태=생성→마지막 활동(초). llm_profile: 작업 planner/worker 가 쓰는 설정 이름, (활성 설정)=전역 활성 설정을 따름."),
    ("(激活配置)", "(활성 설정)"),
    ("#%d(已删除)", "#%d(삭제됨)"),
    ("列出可用的 LLM 配置(profile)：id、名称、模型、格式、是否为当前激活配置。用 id 给 spawn_task 的 llm_profile_id 参数指定子任务专属 LLM（如侦察用便宜模型、利用用强模型）。不含 API Key。", "사용 가능한 LLM 설정(profile)을 나열합니다: id, 이름, 모델, 형식, 현재 활성 설정 여부. id 를 spawn_task 의 llm_profile_id 매개변수에 지정해 하위 작업 전용 LLM 을 쓸 수 있습니다(예: 정찰은 저렴한 모델, 활용은 강한 모델). API Key 는 포함하지 않습니다."),
    ("新建一个子任务并启动探索引擎，返回 task_id。用于把一件事(如一道题/一个目标)派成独立任务。parent_ref 可选：填当前编排关联的父任务 id 做父子关联。", "하위 작업을 새로 만들고 탐색 엔진을 시작한 뒤 task_id 를 반환합니다. 하나의 일(예: 문제 하나/목표 하나)을 독립 작업으로 파견하는 데 사용합니다. parent_ref 는 선택입니다: 현재 오케스트레이션에 연관된 부모 작업 id 를 넣어 부모-자식 연관을 만듭니다."),
    ("任务描述(简短标题)", "작업 설명(짧은 제목)"),
    ("任务目标(要达成什么)", "작업 목표(무엇을 달성할지)"),
    ("可选：父任务 id(做父子关联)", "선택: 부모 작업 id(부모-자식 연관)"),
    ("可选：只读继承的来源任务 id 列表(最多 %d 个)。子任务可只读引用这些任务已探明的资产/结论作为起点；与 parent_ref 的纯父子指针不同，这是内容继承。", "선택: 읽기 전용으로 상속할 출처 작업 id 목록(최대 %d 개). 하위 작업은 이 작업들이 이미 밝혀낸 자산/결론을 읽기 전용으로 참조해 시작점으로 삼을 수 있습니다. parent_ref 의 순수 부모-자식 포인터와 달리 이는 내용 상속입니다."),
    ("可选：指定本子任务 planner/worker 用的 LLM 配置 id(见 list_llm_profiles)；留空则继承父任务、再回退全局激活配置", "선택: 이 하위 작업의 planner/worker 가 쓸 LLM 설정 id 지정(list_llm_profiles 참조); 비우면 부모 작업을 상속하고, 그다음 전역 활성 설정으로 폴백합니다"),
    ("可选：任务级超时(秒)。到点后触发优雅收尾并进入 timeout 终态；留空或 0 = 不限时", "선택: 작업 수준 타임아웃(초). 도달하면 우아한 마무리를 트리거하고 timeout 종료 상태로 들어갑니다; 비우거나 0 = 무제한"),
    ("可选：planner 心跳触发间隔(秒)。距上轮规划结束/任务开始满该值且期间无触发 → 触发一轮规划(兜底死锁 + 唤醒去监督飞行中的 worker)。留空或 0 = 默认 600(10min)；", "선택: planner 하트비트 트리거 간격(초). 지난 계획 라운드 종료/작업 시작 후 이 값에 도달할 때까지 트리거가 없으면 → 계획 라운드를 한 번 트리거합니다(교착 방지 + 비행 중인 worker 감독을 위한 기상). 비우거나 0 = 기본 600(10분);"),
    ("可选：对于简单任务可开启，创建时直接下发一条种子意图(内容=描述+目标)让 worker 免等首轮 planner 直接开跑测试；默认 false(走标准先规划再执行)。", "선택: 단순한 작업이면 켤 수 있습니다. 생성 시 시드 의도 하나를 바로 내려(내용=설명+목표) worker 가 첫 planner 라운드를 기다리지 않고 곧바로 테스트를 시작하게 합니다; 기본 false(표준대로 먼저 계획하고 실행)."),
    ("未命名任务", "이름 없는 작업"),
    ("暂停指定任务(停止其 planner/worker 循环)。", "지정한 작업을 일시정지합니다(planner/worker 루프 중지)."),
    ("要暂停的任务 id", "일시정지할 작업 id"),
    ("读指定任务的探索图总览(同 graph_overview：资产计数/frontier/发现/覆盖等)，用 task_id 指定任务。", "지정한 작업의 탐색 그래프 개요를 읽습니다(graph_overview 와 동일: 자산 수/frontier/발견/커버리지 등). task_id 로 작업을 지정합니다."),
    ("读指定任务的确认漏洞(含 flag/PoC；每条带 id/task_id/intent_id/vulnclass/severity/摘要/状态)，用 task_id 指定任务。", "지정한 작업의 확인된 취약점을 읽습니다(flag/PoC 포함; 각 항목은 id/task_id/intent_id/vulnclass/severity/요약/상태를 가짐). task_id 로 작업을 지정합니다."),
    ("给指定任务注入战略提示(该任务的 planner 下轮生成意图时会读到)。\\n", "지정한 작업에 전략 힌트를 주입합니다(그 작업의 planner 가 다음 의도 생성 시 읽습니다).\\n"),
    ("★优先批量：多条提示放进 hints 数组一次提交（返回 ids 数组，与 hints 等长同序，失败项 id=0）；单条则省略 hints 直接给顶层 text。", "★일괄 처리를 우선하세요: 여러 힌트를 hints 배열에 담아 한 번에 제출합니다(반환 ids 배열은 hints 와 길이가 같고 순서도 같으며 실패 항목 id=0); 단건이면 hints 를 생략하고 최상위 text 만 주면 됩니다."),
    ("任务 id", "작업 id"),
    ("【优先用这个】提示数组，每个元素字段同顶层（text/asset_ids/traffic_refs）。", "【이것을 우선 사용】힌트 배열로, 각 요소 필드는 최상위와 같습니다(text/asset_ids/traffic_refs)."),
    ("提示内容", "힌트 내용"),
    ("[单条] 提示内容", "[단건] 힌트 내용"),
    ("锚定的资产 id（可选，0/1/多个；该任务内的资产 id）", "앵커된 자산 id(선택, 0/1/여러 개; 그 작업 내의 자산 id)"),
    ("看指定任务里某个 work(意图)的执行过程：get_task_worker_trace(task_id, intent_id) 看步骤摘要；再带 step_ids=[...] 取那几步完整内容(一次最多 5 个,多传只返回前 5 个)。", "지정한 작업의 특정 work(의도) 실행 과정을 봅니다: get_task_worker_trace(task_id, intent_id) 로 단계 요약을 보고, step_ids=[...] 를 함께 주면 그 단계들의 전체 내용을 가져옵니다(한 번에 최대 5개, 초과분은 앞 5개만 반환)."),
    ("意图 id(该任务里的 work)", "의도 id(그 작업 내의 work)"),
    ("可选：要取完整内容的步骤 id(一次最多 5 个,多传只返回前 5 个,其余在 omitted_step_ids 里列出)", "선택: 전체 내용을 가져올 단계 id(한 번에 최대 5개, 초과분은 앞 5개만 반환하고 나머지는 omitted_step_ids 에 나열)"),
    ("列出指定任务里跑过哪些 work(意图) + 各自步数，用于发现哪些 work 值得翻看(再用 get_task_worker_trace)。", "지정한 작업에서 어떤 work(의도)가 실행되었는지 + 각 단계 수를 나열합니다. 어떤 work 를 살펴볼 가치가 있는지 발견하는 데 사용합니다(그다음 get_task_worker_trace)."),
    ("在指定任务里按关键字搜索所有 work 的执行过程(返回命中步骤摘要 + intent_id)。", "지정한 작업에서 키워드로 모든 work 의 실행 과정을 검색합니다(일치 단계 요약 + intent_id 반환)."),
    ("搜索关键字", "검색 키워드"),
    ("读指定任务里某个探索图节点的完整内容(发现/事实/意图/目标：摘要 + 详情/证据/PoC)。id 为探索节点 id(如 report_finding 返回、或 list_task_findings 里的 id)。写漏洞报告前用它取该漏洞的完整证据。", "지정한 작업의 특정 탐색 그래프 노드 전체 내용을 읽습니다(발견/사실/의도/목표: 요약 + 세부/증거/PoC). id 는 탐색 노드 id 입니다(report_finding 이 반환하거나 list_task_findings 에 있는 id). 취약점 보고서를 쓰기 전에 이 도구로 해당 취약점의 전체 증거를 가져오세요."),
    ("探索图节点 id(非资产 id)", "탐색 그래프 노드 id(자산 id 아님)"),
    ("为已登记的漏洞写入/更新【详细报告】(Markdown 全文,整段覆盖旧内容)。finding_id 传 report_finding 返回的那个 id(\\\"finding recorded: <id>\\\" 里的数字)。报告建议包含:漏洞概述、影响与危害、复现步骤、证据/PoC、修复建议。", "이미 등록된 취약점에【상세 보고서】를 쓰거나 갱신합니다(Markdown 전문, 기존 내용을 통째로 덮어씀). finding_id 에는 report_finding 이 반환한 그 id(\\\"finding recorded: <id>\\\" 의 숫자)를 전달하세요. 보고서에는 취약점 개요, 영향과 위험, 재현 단계, 증거/PoC, 수정 권고를 포함하는 것을 권장합니다."),
    ("目标漏洞 id(report_finding 返回的 id)", "대상 취약점 id(report_finding 이 반환한 id)"),
    ("详细报告全文,Markdown 格式", "상세 보고서 전문, Markdown 형식"),
    ("get_finding_traffic 返回的证据 version；用于防止报告覆盖新的证据变更", "get_finding_traffic 이 반환한 증거 version; 보고서가 새로운 증거 변경을 덮어쓰는 것을 방지합니다"),
    ("上面刚有一个漏洞被 report_finding 登记。请读取返回 JSON 的 finding_id（独立漏洞记录 ID）与 finding_node_id（探索节点 ID），", "방금 위에서 report_finding 으로 취약점 하나가 등록되었습니다. 반환 JSON 의 finding_id(독립 취약점 기록 ID)와 finding_node_id(탐색 노드 ID)를 읽으세요,"),
    ("先用 get_finding_traffic(finding_id) 读取当前证据清单及其 version（空清单是正常情况，照常写报告）；", "먼저 get_finding_traffic(finding_id) 로 현재 증거 목록과 그 version 을 읽으세요(빈 목록은 정상이며, 평소대로 보고서를 쓰면 됩니다);"),
    ("若运行指引启用自动绑定，在读取前先核实并关联本次漏洞的流量。节点详情使用 finding_node_id。", "실행 안내에서 자동 연결이 켜져 있으면 읽기 전에 이번 취약점의 트래픽을 먼저 확인하고 연결하세요. 노드 상세에는 finding_node_id 를 사용하세요."),
    ("最后调用 update_finding_report(finding_id=finding_node_id, report, evidence_version=实际读取版本) 保存，", "마지막으로 update_finding_report(finding_id=finding_node_id, report, evidence_version=실제 읽은 버전) 를 호출해 저장하세요,"),
    ("evidence_version 必须传，否则报告会被永久标记为待更新。不要混用两种编号。", "evidence_version 은 반드시 전달해야 하며, 그렇지 않으면 보고서가 영구히 갱신 대기로 표시됩니다. 두 가지 번호를 혼용하지 마세요."),
    ("上面刚有一个漏洞被 report_finding 登记。请从触发上下文里取出 finding_id", "방금 위에서 report_finding 으로 취약점 하나가 등록되었습니다. 트리거 컨텍스트에서 finding_id 를 꺼내세요"),
    ("（工具返回 \\\"finding recorded: <id>\\\" 里的数字）与任务 id，按你的职责撰写该漏洞的详细报告，", "(도구가 반환한 \\\"finding recorded: <id>\\\" 의 숫자)와 작업 id 를 확인하고, 맡은 역할에 따라 해당 취약점의 상세 보고서를 작성한 뒤,"),
    ("最后调用 update_finding_report(finding_id, report) 保存。", "마지막으로 update_finding_report(finding_id, report) 를 호출해 저장하세요."),
    ("报告撰写", "보고서 작성"),
    ("漏洞详细报告撰写：发现漏洞时自动触发，查取证据与执行过程后写 Markdown 报告并回写。", "취약점 상세 보고서 작성: 취약점 발견 시 자동 실행되며, 증거와 실행 과정을 조회한 뒤 Markdown 보고서를 작성하고 되돌려 기록합니다."),
]


def main() -> int:
    check = "--check" in sys.argv
    root = pathlib.Path(__file__).resolve().parent.parent
    han = re.compile(r"[\u4e00-\u9fff]")
    rel = "server/orchestration.go"
    path = root / rel
    text = path.read_text(encoding="utf-8")
    before = text
    applied = 0
    for zh, ko in sorted(PAIRS, key=lambda p: len(p[0]), reverse=True):
        if zh in text:
            text = text.replace(zh, ko)
            applied += 1
    if not check and text != before:
        path.write_text(text, encoding="utf-8")
    print(f"applied {applied} replacements (check={check})")

    leftover = []
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
