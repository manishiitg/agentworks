"""PLAT-856-383: the deploy admission scan must never treat user files as platform records."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[2]


def module(relative):
    spec = importlib.util.spec_from_file_location(relative.replace('/', '_'), REPO / relative)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


class CrewPrivateStateHooks(unittest.TestCase):
    def test_admission_scan_reads_private_records_not_same_named_user_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            docs, state = base / 'docs', base / 'state'
            user, private = docs / 'Crew/example', docs / '_system/crew-data/id'
            for folder in (user, private, state / 'ownership'):
                folder.mkdir(parents=True)
            person = {
                'product.json': {'identity': {'role': 'Person role'}, 'description': 'Person text'},
                'workflow.json': {'id': 'person-file', 'run_retention_count': 12, 'capabilities': {'browser_mode': 'auto'}},
            }
            for name, value in person.items():
                (user / name).write_text(json.dumps(value))
            (private / 'product.json').write_text(json.dumps({'id': 'id', 'owner_id': 'owner', 'identity': {'role': 'Platform role'}, 'description': 'Purpose'}))
            (private / 'workflow.json').write_text(json.dumps({'id': 'id', 'capabilities': {'browser_mode': 'auto'}}))
            (state / 'ownership/projects.json').write_text(json.dumps({'projects': {'work/example': {'product': 'work', 'folder': 'example', 'project_id': 'id', 'owner_id': 'owner'}}}))
            paths = module('deploy/common/admission_scan.py').manifests(docs)
            self.assertEqual(set(paths), {private / 'product.json', private / 'workflow.json'})


if __name__ == '__main__':
    unittest.main()
