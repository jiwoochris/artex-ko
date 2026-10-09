import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


def load_client():
    spec = importlib.util.spec_from_file_location('lab_client', Path(__file__).with_name('lab_get_client.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class EvidenceTests(unittest.TestCase):
    def test_two_executions_preserve_both_response_bodies(self):
        with tempfile.TemporaryDirectory() as directory:
            paths = []
            for payload in (b'first response', b'second response'):
                with patch.dict('os.environ', {'ARTEX_EVIDENCE_DIR': directory}):
                    client = load_client()
                response = io.BytesIO(payload)
                response.code = 200
                response.headers = {}
                with patch.object(client._opener, 'open', return_value=response):
                    _, _, body, row = client.req('/api/me')
                self.assertEqual(body, payload)
                paths.append(Path(row['body_file']))
            self.assertNotEqual(paths[0], paths[1])
            self.assertEqual(paths[0].read_bytes(), b'first response')
            self.assertEqual(paths[1].read_bytes(), b'second response')
            for path in paths:
                records = [json.loads(x) for x in (path.parent.parent / 'requests.jsonl').read_text().splitlines()]
                self.assertEqual(len(records), 1)
                self.assertEqual(records[0]['body_file'], str(path))

    def test_existing_body_is_never_replaced(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.dict('os.environ', {'ARTEX_EVIDENCE_DIR': directory}):
                client = load_client()
            target = client.D / 'bodies' / 'r001.body'
            target.parent.mkdir(parents=True)
            target.write_bytes(b'original')
            response = io.BytesIO(b'replacement')
            response.code = 200
            response.headers = {}
            with patch.object(client._opener, 'open', return_value=response):
                with self.assertRaises(FileExistsError):
                    client.req('/api/me')
            self.assertEqual(target.read_bytes(), b'original')

    def test_wrong_origin_and_exhausted_budget_never_send(self):
        client = load_client()
        with patch.object(client._opener, 'open') as send:
            for path in ['https://example.com/', '//example.com/', 'http://user@127.0.0.1:18880/']:
                with self.assertRaises(ValueError):
                    client.req(path)
            client._count = client.MAX_REQ
            with self.assertRaises(RuntimeError):
                client.req('/api/me')
            send.assert_not_called()


if __name__ == '__main__':
    unittest.main()
