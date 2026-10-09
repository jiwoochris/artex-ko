#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Korean-ize the tool descriptions/schemas defined outside agent/tools*.go.

Covers the platform tools (create/update skill, custom tool, MCP), the finding
traffic tools, and the traffic search/get/blob tools. These rows reach the
dashboard /system/tools page and the LLM prompt, and they are seeded once, so
the code change needs the accompanying refreshBuiltinToolDescriptionsKo backfill
to reach existing installs.

Only string literals containing Han characters are replaced; Chinese code
comments are untouched. Go escapes (`\\n`) and format verbs (%d) are preserved.

Usage:
  python3 scripts/translate-zh-tools-server.py [--check]
"""
import pathlib
import re
import sys

PAIRS = {
    "server/platform_tools.go": [
        ("按 host 精确删除资产：删掉该 host 的域名/子域名，以及其下的服务(service)、接口(endpoint)。\\n", "host 기준으로 자산을 정확히 삭제합니다: 해당 host 의 도메인/하위 도메인과 그 아래의 서비스(service)·인터페이스(endpoint)를 삭제합니다.\\n"),
        ("host 完全匹配(小写、去空格)，不是模糊/通配。\\n", "host 는 완전 일치(소문자, 공백 제거)이며 퍼지/와일드카드가 아닙니다.\\n"),
        ("传根域名(如 example.com)会连带删除它的子域名及其服务/接口；传子域名(如 a.example.com)或 IP 只删该 host 自身及其服务/接口。\\n", "루트 도메인(예: example.com)을 전달하면 그 하위 도메인과 서비스/인터페이스도 함께 삭제됩니다. 하위 도메인(예: a.example.com)이나 IP 를 전달하면 해당 host 자신과 그 서비스/인터페이스만 삭제됩니다.\\n"),
        ("⚠️ 硬删除、作用于全局资产库(跨任务共享)、不可撤销。", "⚠️ 하드 삭제이며 전역 자산 라이브러리(작업 간 공유)에 적용되고 되돌릴 수 없습니다."),
        ("要删除的 host：域名/子域名/IP。完全匹配，如 example.com 或 a.example.com 或 1.2.3.4", "삭제할 host: 도메인/하위 도메인/IP. 완전 일치 예: example.com 또는 a.example.com 또는 1.2.3.4"),
        ("创建一个新 skill(写 SKILL.md，agentskills.io 规范)。name 小写字母/数字/连字符。", "새 skill 을 생성합니다(SKILL.md 작성, agentskills.io 규격). name 은 소문자/숫자/하이픈."),
        ("skill 名(小写字母开头，字母/数字/连字符)", "skill 이름(소문자로 시작, 영문/숫자/하이픈)"),
        ("skill 描述(必填，说明它做什么/何时用)", "skill 설명(필수, 무엇을 하고 언제 쓰는지 설명)"),
        ("Markdown 正文说明(可选)", "Markdown 본문 설명(선택)"),
        ("写/覆盖某个 skill 内的一个文件(默认 SKILL.md)。用于修改技能内容或加脚本/引用。", "skill 내부의 파일 하나를 쓰거나 덮어씁니다(기본 SKILL.md). 스킬 내용을 수정하거나 스크립트/참조를 추가하는 데 사용합니다."),
        ("skill 名", "skill 이름"),
        ("相对路径(可选，默认 SKILL.md，如 scripts/run.py)", "상대 경로(선택, 기본 SKILL.md, 예: scripts/run.py)"),
        ("文件完整内容", "파일 전체 내용"),
        ("发给模型的描述", "모델에게 보낼 설명"),
        ("shell | command | script(仅Python) | http。shell=bash 环境声明(仅告知模型该工具可在 bash 中直接调用，无需 exec/schema)；其余三种需提供 exec", "shell | command | script(Python 전용) | http. shell=bash 환경 선언(모델에게 이 도구를 bash 에서 직접 호출할 수 있다고 알릴 뿐이며 exec/schema 불필요); 나머지 세 가지는 exec 필요"),
        ("执行规格(shell 类型不需要): command→{command}; script→{code}; http→{method,url,headers,body,proxy,use_recording_proxy}", "실행 사양(shell 유형은 불필요): command→{command}; script→{code}; http→{method,url,headers,body,proxy,use_recording_proxy}"),
        ("参数 JSON-Schema(shell/command/script 可留空; http 必填且需含 properties)", "매개변수 JSON-Schema(shell/command/script 는 비워 둘 수 있음; http 는 필수이며 properties 포함 필요)"),
        ("绑定的 agent key(可选)", "바인딩할 agent key(선택)"),
        ("是否延迟(shell 类型无效；仅 command/script/http 的不常用工具才开)", "지연 여부(shell 유형은 무효; command/script/http 중 자주 쓰지 않는 도구만 켜세요)"),
        ("是否启用(默认 true)", "활성화 여부(기본 true)"),
        ("【重要】当安装一些平台没有的工具时，调用该工具将安装的工具放入平台中，让平台可以调用！创建一个自定义工具(shell/command/script/http)。shell=bash 环境声明，只需 key+description+agents，无需 exec/schema。", "【중요】플랫폼에 없는 도구를 설치했을 때, 이 도구를 호출해 설치한 도구를 플랫폼에 등록하여 플랫폼이 호출할 수 있게 합니다! 사용자 정의 도구(shell/command/script/http)를 생성합니다. shell=bash 환경 선언으로 key+description+agents 만 필요하며 exec/schema 는 불필요합니다."),
        ("工具 key(小写字母开头，字母/数字/下划线)", "도구 key(소문자로 시작, 영문/숫자/밑줄)"),
        ("修改一个已有的自定义工具(按 key)。", "기존 사용자 정의 도구를 수정합니다(key 기준)."),
        ("要修改的自定义工具 key", "수정할 사용자 정의 도구 key"),
        ("MCP 服务器名", "MCP 서버 이름"),
        ("stdio 的启动命令(如 npx)", "stdio 의 시작 명령(예: npx)"),
        ("命令参数数组", "명령 인수 배열"),
        ("环境变量 {KEY:VALUE}", "환경 변수 {KEY:VALUE}"),
        ("http/sse 的 URL", "http/sse 의 URL"),
        ("是否启用(默认 true)", "활성화 여부(기본 true)"),
        ("http: 跳过 TLS 证书校验(自签证书时置 true, 默认 false)", "http: TLS 인증서 검증 건너뛰기(자체 서명 인증서면 true, 기본 false)"),
        ("要修改的 MCP 服务器 id", "수정할 MCP 서버 id"),
        ("创建一个 MCP 服务器(stdio/http/sse)。创建后其工具需按 agent 可见性授权。", "MCP 서버를 생성합니다(stdio/http/sse). 생성 후 그 도구들은 agent 별 가시성으로 인가해야 합니다."),
        ("修改一个已有的 MCP 服务器(按 id)。", "기존 MCP 서버를 수정합니다(id 기준)."),
    ],
    "server/finding_workflow.go": [
        ("查询记录代理已抓取的目标流量（必须指定 host，可再按 URL 子串或正文关键词过滤）。body_contains 会在已抓取的请求/响应头与正文中做全文搜索，支持任意子串和中文（至少 3 个字符），可用来找响应里的密码、密钥、报错、内网地址等。仅返回极轻量索引(id/method/url/status/resp_len)，不含任何响应内容。默认只返回 3 条、每页最多 10 条；结果多时用 page 翻页（page=0 起）；要看某条的请求/响应原文用 traffic_get(id)。回看已访问资源、找端点先用它，避免重复 curl 同一 URL。", "기록 프록시가 이미 수집한 대상 트래픽을 조회합니다(host 필수 지정, URL 하위 문자열이나 본문 키워드로 추가 필터 가능). body_contains 는 수집된 요청/응답 헤더와 본문에서 전문 검색을 수행하며 임의 하위 문자열과 한글을 지원합니다(최소 3자). 응답 속 비밀번호·키·오류·내부 주소 등을 찾는 데 쓸 수 있습니다. 매우 가벼운 색인(id/method/url/status/resp_len)만 반환하고 응답 내용은 포함하지 않습니다. 기본 3건, 페이지당 최대 10건을 반환하며 결과가 많으면 page 로 페이징합니다(page=0 부터). 특정 건의 요청/응답 원문을 보려면 traffic_get(id) 를 사용하세요. 이미 방문한 리소스를 다시 보거나 엔드포인트를 찾을 때 먼저 이것을 써서 같은 URL 을 중복 curl 하는 것을 피하세요."),
        ("可选：本任务中对应此漏洞的 hint ID；读取该提示保存的 traffic_refs 一并绑定，无提示时省略", "선택: 이 작업에서 이 취약점에 대응하는 hint ID; 그 힌트에 저장된 traffic_refs 를 읽어 함께 연결합니다. 힌트가 없으면 생략하세요"),
        ("提示内容", "힌트 내용"),
        ("为已登记漏洞补绑经核实的真实 HTTP 流量。finding_id 使用独立漏洞记录 ID；不要传探索节点 ID。同批引用全部成功或全部失败，重复引用不覆盖已有说明。补绑会使已有报告标记待更新；不要为补包重新探测或重复创建漏洞。", "이미 등록된 취약점에 확인된 실제 HTTP 트래픽을 추가 연결합니다. finding_id 는 독립 취약점 기록 ID 를 사용하며 탐색 노드 ID 를 전달하지 마세요. 같은 배치의 참조는 전부 성공하거나 전부 실패하며, 중복 참조는 기존 설명을 덮어쓰지 않습니다. 추가 연결은 기존 보고서를 갱신 대기로 표시합니다. 패킷 보충을 위해 재탐지하거나 취약점을 중복 생성하지 마세요."),
        ("独立漏洞记录 ID，从 list_task_findings / get_task_node_detail 的 finding_id 字段读取", "독립 취약점 기록 ID 로, list_task_findings / get_task_node_detail 의 finding_id 필드에서 읽습니다"),
    ],
    "traffic/traffic.go": [
        ("查询记录代理已抓取的目标流量（必须指定 host；支持裸主机、主机:端口或完整 URL，可再按 URL 子串或正文关键词过滤）。指定端口时只返回该服务的流量，避免同一 IP 的不同端口串包。body_contains 会在已抓取的请求/响应头与正文中做全文搜索，支持任意子串和中文（至少 3 个字符）。仅返回极轻量索引(id/method/url/status/resp_len)，不含响应内容；结果非空后必须用 traffic_get 逐条核实请求/响应，再把确实支持当前漏洞的 ID 交给 bind_finding_traffic。默认只返回 3 条、每页最多 10 条；结果多时用 page 翻页。", "기록 프록시가 이미 수집한 대상 트래픽을 조회합니다(host 필수 지정; 호스트만, 호스트:포트, 또는 전체 URL 을 지원하며 URL 하위 문자열이나 본문 키워드로 추가 필터 가능). 포트를 지정하면 해당 서비스의 트래픽만 반환하여 같은 IP 의 다른 포트가 섞이는 것을 막습니다. body_contains 는 수집된 요청/응답 헤더와 본문에서 전문 검색을 수행하며 임의 하위 문자열과 한글을 지원합니다(최소 3자). 매우 가벼운 색인(id/method/url/status/resp_len)만 반환하고 응답 내용은 포함하지 않습니다. 결과가 비어 있지 않으면 반드시 traffic_get 으로 요청/응답을 항목별로 확인한 뒤, 현재 취약점을 실제로 뒷받침하는 ID 를 bind_finding_traffic 에 전달하세요. 기본 3건, 페이지당 최대 10건을 반환하며 결과가 많으면 page 로 페이징합니다."),
        ("按主机过滤（必填；如 '107.172.96.177'、'107.172.96.177:8082' 或 'http://107.172.96.177:8082/path'）", "호스트로 필터(필수; 예: '107.172.96.177', '107.172.96.177:8082', 'http://107.172.96.177:8082/path')"),
        ("URL 子串过滤（可选，如 'api' / 'login'）", "URL 하위 문자열 필터(선택, 예: 'api' / 'login')"),
        ("正文全文搜索（可选，至少 3 个字符），匹配请求/响应的头与正文，如 'password' / 'root:x:0' / '内网测试'", "본문 전문 검색(선택, 최소 3자)으로 요청/응답의 헤더와 본문을 매칭합니다. 예: 'password' / 'root:x:0' / '내부망 테스트'"),
        ("每页条数，默认 3，最大 10", "페이지당 건수, 기본 3, 최대 10"),
        ("页码，从 0 开始，默认 0（按 ts 倒序分页）", "페이지 번호, 0 부터 시작, 기본 0(ts 내림차순 페이징)"),
        ("无匹配流量。", "일치하는 트래픽이 없습니다."),
        ("按 id 取一条已抓流量的请求/响应原文（过大会截断）。配合 traffic_search 用，避免重复 curl。", "id 로 수집된 트래픽 한 건의 요청/응답 원문을 가져옵니다(너무 크면 잘림). traffic_search 와 함께 사용하여 중복 curl 을 피하세요."),
        ("traffic_search 返回的 id", "traffic_search 가 반환한 id"),
        ("分段读取超大请求/响应体的原文。traffic_get 里显示为 '…[truncated] @blob sha256:<hash>' 的部分即存放于此，把该 hash 传进来即可取完整内容。单次最多返回 8KB，用 offset 继续往后读（返回结果会给出总长度）。适合翻阅备份文件、源码泄露、大 JSON 导出等超过内联阈值的响应。", "초대형 요청/응답 본문의 원문을 분할 조회합니다. traffic_get 에서 '…[truncated] @blob sha256:<hash>' 로 표시된 부분이 여기에 저장되어 있으며, 그 hash 를 전달하면 전체 내용을 가져옵니다. 한 번에 최대 8KB 를 반환하고 offset 으로 계속 읽습니다(반환 결과에 총 길이가 표시됩니다). 백업 파일, 소스 유출, 대용량 JSON 내보내기 등 인라인 임계값을 넘는 응답을 열람하는 데 적합합니다."),
        ("traffic_get 中 @blob sha256: 后面的 64 位十六进制值", "traffic_get 의 @blob sha256: 뒤에 오는 64자리 16진수 값"),
        ("起始字节偏移，默认 0", "시작 바이트 오프셋, 기본 0"),
        ("本次读取字节数，默认且最大 8192", "이번 읽기 바이트 수, 기본이자 최대 8192"),
        ("偏移 %d 已超出内容长度（总长 %d 字节）。", "오프셋 %d 이(가) 내용 길이를 초과했습니다(총 길이 %d 바이트)."),
        ("[offset=%d 本次=%d 总长=%d]\\n", "[offset=%d 이번=%d 총길이=%d]\\n"),
        ("二进制内容，以十六进制展示前 512 字节：\\n", "바이너리 내용, 앞 512 바이트를 16진수로 표시:\\n"),
        ("\\n... [截断，共 %d 字节；完整在流量文件树] ...", "\\n... [잘림, 총 %d 바이트; 전체는 트래픽 파일 트리에 있음] ..."),
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
