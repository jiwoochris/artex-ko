"""Bounded GET-only helper for an authorized loopback lab; one evidence directory per run."""
import urllib.request, urllib.error, time, json, hashlib, threading, os, uuid
from pathlib import Path
from urllib.parse import urlsplit, urljoin

ORIGIN = 'http://127.0.0.1:18880'
RUN_ID = uuid.uuid4().hex
D = Path(os.environ.get('ARTEX_EVIDENCE_DIR', 'evidence')).resolve() / RUN_ID
MAX_REQ = 60          # 최대 60요청
MAX_SECONDS = 600.0   # 최대 10분
MIN_INTERVAL = 1.0    # 1초 간격
MAX_BODY = 1048576    # 1MiB
_lock = threading.Lock()
_start = None
_last = 0.0
_count = 0

class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None  # 자동 redirect 금지 → 3xx는 그대로 반환

_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), _NoRedirect)

def req(path, role=None):
    """GET 전용 제어 요청. role은 X-Dev-User 값(의도된 개발 인증). (status, headers, body_bytes, meta) 반환."""
    global _start, _last, _count
    with _lock:
        if _start is None:
            _start = time.time()
        url = urljoin(ORIGIN + '/', path)
        p = urlsplit(url)
        if (p.scheme, p.hostname, p.port) != ('http', '127.0.0.1', 18880):
            raise ValueError('origin 위반')
        if p.username or p.password:
            raise ValueError('userinfo 금지')
        if _count >= MAX_REQ:
            raise RuntimeError('요청 수 예산(60) 초과')
        if time.time() - _start >= MAX_SECONDS:
            raise RuntimeError('시간 예산(10분) 초과')
        wait = MIN_INTERVAL - (time.time() - _last)
        if wait > 0:
            time.sleep(wait)
        _last = time.time()
        _count += 1
        n = _count
        headers = {'Accept': 'application/json'}
        if role:
            headers['X-Dev-User'] = role
        truncated = False
        t0 = time.time()
        try:
            try:
                r = _opener.open(urllib.request.Request(url, headers=headers, method='GET'), timeout=15)
            except urllib.error.HTTPError as e:
                r = e
            clen = r.headers.get('Content-Length')
            with r:
                body = r.read(MAX_BODY + 1)
            if len(body) > MAX_BODY:
                body = body[:MAX_BODY]
                truncated = True
            status = r.code
            rh = {'content-type': r.headers.get('Content-Type'),
                  'content-length': clen, 'location': r.headers.get('Location')}
        except Exception as e:
            body = str(e).encode()
            status = 0
            rh = {}
        dt = round(time.time() - t0, 3)
        bdir = D / 'bodies'
        bdir.mkdir(parents=True, exist_ok=True)
        bpath = bdir / f'r{n:03}.body'
        with bpath.open('xb') as output:
            output.write(body)
        row = {'run_id': RUN_ID, 'id': n, 'ts': round(time.time(), 3), 'elapsed_s': round(time.time() - _start, 1),
               'method': 'GET', 'url': url, 'role': role, 'status': status,
               'length': len(body), 'truncated': truncated, 'sha256': hashlib.sha256(body).hexdigest(),
               'resp_headers': rh, 'duration_s': dt, 'body_file': str(bpath)}
        with (D / 'requests.jsonl').open('a') as o:
            o.write(json.dumps(row, ensure_ascii=False) + '\n')
        print(n, 'GET', role or '-', path, '->', status, f'len={len(body)} {dt}s', flush=True)
        return status, rh, body, row

def budget():
    return {'run_id': RUN_ID, 'directory': str(D), 'count': _count, 'max_req': MAX_REQ, 'start': _start,
            'elapsed_s': None if _start is None else round(time.time() - _start, 1)}
