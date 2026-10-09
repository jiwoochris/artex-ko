# 격리 점검 증거 보존

`scripts/lab_get_client.py`는 허가된 로컬 합성 데이터 환경에서 쓰는 GET 전용 보조 모듈이다. ARTEX 본체의 자동 실행 기능은 아니다.

```sh
export ARTEX_EVIDENCE_DIR=/path/to/task/evidence
export PYTHONPATH=/path/to/artex-ko/scripts
python3 - <<'PY'
from lab_get_client import req, budget
status, headers, body, record = req('/api/me', role='synthetic-user')
print(budget())
PY
```

- 대상은 `http://127.0.0.1:18880`으로 고정한다. 환경 프록시·자동 리다이렉트는 사용하지 않는다.
- 각 실행은 새 UUID 하위 폴더에 `requests.jsonl`과 `bodies/r001.body` 등을 저장한다. 로그의 `run_id`와 `id`를 함께 식별자로 사용한다.
- 재실행은 새 실행이다. 과거 폴더를 재사용하거나 기존 로그에 번호를 이어 붙이지 않는다. 기존 본문 파일이 충돌하면 `xb` 모드가 실패하므로 덮어쓰지 않는다.
- 요청 60회·10분·동시 1·최소 1초 간격·응답 최대 1 MiB 제한은 **실행별**이다. 여러 프로세스의 합산 예산이나 잠금은 제공하지 않는다. 동일 대상을 여러 실행으로 병렬 점검하지 않는다.
- 본문 해시와 절단 여부를 로그에 기록한다. 전송 실패는 status 0으로 기록하며 성공으로 판정하지 않는다. 기록은 로컬 민감 데이터일 수 있으므로 저장소에 커밋하지 않는다.
- 기존 임시 `client.py` 대신 이 모듈을 import하는 새 점검부터 적용된다. 과거 증거·중단된 작업은 변경하지 않는다.

회귀 검사: `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p test_lab_get_client.py`
