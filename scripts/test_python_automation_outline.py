"""PLAT-746-440: AST inspection is inert; labels never change Python results."""
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]

def module(name, file):
    spec = importlib.util.spec_from_file_location(name, file)
    loaded = importlib.util.module_from_spec(spec)
    sys.modules[name] = loaded
    spec.loader.exec_module(loaded)
    return loaded

class StepOutline(unittest.TestCase):
    def test_inert_outline_resolves_aliases_and_agent_task_without_inventing_dynamic_order(self):
        inspector = module('source_inspector', ROOT/'agent_go/pkg/relaypython/inspect_source.py')
        files = {'actions.py': 'raise RuntimeError("never execute inspection")\nfrom agentworks import step as stage\n@stage("Collect")\ndef collect(): return 7\n',
                 'prompts.py': 'TASK = \"Read the collected report\"\n',
                 'main.py': 'raise RuntimeError("never execute inspection")\nfrom actions import collect as gather\nfrom agentworks import agent as worker\nfrom prompts import TASK\ndef main():\n    gather()\n    worker("Review", TASK)\n    worker("Dynamic", build_task())\n    for item in source():\n        gather()\n'}
        graph = inspector.inspect_automation_source(files)
        self.assertEqual(graph['errors'], [])
        nodes = graph['nodes']
        self.assertEqual([node['label'] for node in nodes], ['Collect','Review','Dynamic','Other code'])
        self.assertNotIn('description', nodes[0])
        self.assertEqual(nodes[1]['description'], 'Read the collected report')
        self.assertEqual(nodes[2]['description'], 'built at run time')
        self.assertEqual(nodes[0]['call'], 'actions.py:collect@main.py:6')
        self.assertTrue(inspector.inspect_automation_source({'main.py':'def main(:'})['errors'])

    def test_dynamic_model_override_and_collapsed_step_keep_their_execution_identity(self):
        # Codex1730 P2: an unresolved override is not the default model, and
        # collapsing one agent must keep the enclosing decorated step receipt.
        inspector = module('review_outline', ROOT/'agent_go/pkg/relaypython/inspect_source.py')
        source = 'from agentworks import agent, step\n@step("Review")\ndef review():\n    return agent("Worker", "Review it", model=MODEL)\ndef main():\n    review()\n    agent("Dynamic tier", "Work", tier=INPUT["tier"])\n    agent("Literal", "Work", model="example/model")\n'
        graph = inspector.inspect_automation_source({'main.py':source})
        self.assertEqual(graph['errors'],[])
        nodes = [node for node in graph['nodes'] if not node.get('role')]
        self.assertEqual(nodes[0]['type'],'agent')
        self.assertEqual(nodes[0]['step_call'],'main.py:review@main.py:6')
        self.assertIn('model',nodes[0]['settings'])
        self.assertIsNone(nodes[0]['settings']['model'])
        self.assertIsNone(nodes[1]['settings']['tier'])
        self.assertEqual(nodes[2]['settings']['model'],'example/model')

    def test_decorator_preserves_results_errors_repeated_calls_and_source_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            previous = os.environ.get('WORKFLOW_RUN_DIR')
            os.environ['WORKFLOW_RUN_DIR'] = directory
            try:
                helpers = module('agentworks', ROOT/'workspace/security/runtime/agentworks/__init__.py')
            finally:
                if previous is None: os.environ.pop('WORKFLOW_RUN_DIR',None)
                else: os.environ['WORKFLOW_RUN_DIR'] = previous
            recorded = []
            def call(action, args, key, redo):
                recorded.append((action,args))
                return helpers.Reply(ok=True,conversation_id='activity-'+str(len(recorded)))
            helpers._call = call
            source = 'from agentworks import step\n@step("Collect")\ndef collect(value):\n    if value < 0: raise ValueError("original failure")\n    return value + 1\ndef main():\n    return collect(7)\n'
            scope = {'__name__':'fixture'}
            exec(compile(source,'main.py','exec'),scope)
            self.assertEqual(scope['main'](),8)
            self.assertEqual(scope['main'](),8)
            with self.assertRaisesRegex(ValueError,'original failure'):scope['collect'](-1)
            starts = [args for action,args in recorded if args['phase']=='start']
            finishes = [args for action,args in recorded if args['phase']=='finish']
            self.assertEqual(len(starts),3)
            self.assertEqual([args['ok'] for args in finishes],[True,True,False])
            self.assertEqual(starts[0]['source_call'],'main.py:collect@main.py:7')
            self.assertNotIn('value',starts[0])
            def offline(*args): raise helpers.AutomationError('offline')
            helpers._call = offline
            self.assertEqual(scope['main'](),8)
            with self.assertRaisesRegex(ValueError,'original failure'):scope['collect'](-1)

if __name__ == '__main__':
    unittest.main()
