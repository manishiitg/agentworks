"""PR498 / PLAT-746: converter refusals use the real refactor binary, before writes.
Run with AGENTWORKS_TEST_AGENT_BINARY pointing to the isolated preview build.
"""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('migrate-workflow-input-contracts.py')
spec = importlib.util.spec_from_file_location('input_conversion', SCRIPT)
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


def function(name, field):
    return {'name': name, 'action': {'type': 'run'}, 'input': {},
            'input_schema': {'type': 'object', 'properties': {field: {'type': 'integer', 'minimum': 1}}, 'required': [field]}}


class InputContractMigration(unittest.TestCase):
    def setUp(self):
        self.binary = os.environ.get('AGENTWORKS_TEST_AGENT_BINARY')
        if not self.binary:
            self.skipTest('set AGENTWORKS_TEST_AGENT_BINARY to the isolated refactor build')
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.workflow = self.root/'Workflow/example'
        (self.workflow/'variables').mkdir(parents=True)
        self.write('workflow.json', {'id': 'example'})

    def write(self, path, value):
        (self.workflow/path).write_text(json.dumps(value))

    def hashes(self):
        return {str(p.relative_to(self.root)): hashlib.sha256(p.read_bytes()).hexdigest()
                for p in self.root.rglob('*') if p.is_file()}

    def assert_refused_without_writes(self, expected):
        before = self.hashes()
        result = subprocess.run(['python3', str(SCRIPT), '--root', str(self.root),
                                 '--agent-binary', self.binary, '--apply'], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(expected, result.stdout)
        self.assertEqual(before, self.hashes())

    def test_incompatible_defaults_and_presets_refused_before_apply(self):
        self.write('functions.json', {'functions': [function('count', 'count')]})
        cases = [({'variables': [{'name': 'count', 'value': 'many'}]}, 'workflow Inputs'),
                 ({'variables': [{'name': 'count', 'value': '2'}], 'groups': [{'name': 'weekly', 'values': {'count': '0'}}]}, 'workflow Inputs')]
        for definitions, diagnostic in cases:
            with self.subTest(definitions=definitions):
                self.write('variables/variables.json', definitions)
                self.assert_refused_without_writes(diagnostic)
        # Nested JSON constraints must be checked too, not just the top-level type.
        self.write('variables/variables.json', {'variables': [{'name': 'payload', 'value': '{"count":0}'}]})
        nested = function('payload', 'payload')
        nested['input_schema']['properties']['payload'] = {'type': 'object', 'properties': {'count': {'type': 'integer', 'minimum': 1}}, 'required': ['count']}
        self.write('functions.json', {'functions': [nested]})
        self.assert_refused_without_writes('workflow Inputs')

    def test_disjoint_functions_need_values_for_other_required_inputs(self):
        self.write('variables/variables.json', {'variables': [{'name': 'x'}, {'name': 'y'}]})
        functions = [function('first', 'x'), function('second', 'y')]
        self.write('functions.json', {'functions': functions})
        self.assert_refused_without_writes('needed to start')
        # Preserve the author-selected contracts: fixed values supply inputs
        # outside each function, without silently widening either public schema.
        functions[0]['input'] = {'inputs': {'y': '2'}}
        functions[1]['input'] = {'inputs': {'x': '3'}}
        self.write('functions.json', {'functions': functions})
        result = subprocess.run(['python3', str(SCRIPT), '--root', str(self.root),
                                 '--agent-binary', self.binary, '--apply'], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        converted = json.loads((self.workflow/'functions.json').read_text())['functions']
        self.assertEqual([fn['inputs'] for fn in converted], [['x'], ['y']])
        self.assertTrue(all('input_schema' not in fn for fn in converted))
        changes, errors = m.convert(self.root, self.binary)
        self.assertEqual((changes, errors), ({}, []))


if __name__ == '__main__':
    unittest.main()
