"""PLAT-746-428: same-ID conversion preserves history; conflicting DBs refuse."""
import hashlib
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('migration', Path(__file__).with_name('migrate-workflow-to-python.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


def hashes(path):
    return {str(p.relative_to(path)):hashlib.sha256(p.read_bytes()).hexdigest() for p in path.rglob('*') if p.is_file()}


class SameIdentityConversion(unittest.TestCase):
    def test_initialize_convert_and_refuse_conflicts_without_history_moves(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            docs, state, source = root/'docs', root/'state', root/'source'
            self.assertEqual(m.plan(docs, root/'empty-state', True)[2]['binding_count'], 0)
            crew = docs/'Crew/example'
            private = docs/'_system/crew-data/crew'
            runtime = private/'workflows/task'
            code = crew/'.agentworks/code/task'
            definitions = crew/'.agentworks/workflows/task'
            for p in [runtime/'db', runtime/'runs/old', definitions/'planning', code/'old-step', state/'ownership/crew-executables', source]:
                p.mkdir(parents=True)
            projects_file = state/'ownership/projects.json'
            projects = {'projects': {'work/example': {'product':'work','owner_id':'owner','project_id':'crew','folder':'example'}}}
            projects_file.write_bytes(m.encode(projects))
            binding = {'id':'task','crew_id':'crew','crew_root':'Crew/example','workspace_path':'Workflow/kept-name','kind':'workflow'}
            binding_file = state/'ownership/crew-executables'/(hashlib.sha256(binding['workspace_path'].encode()).hexdigest()+'.json')
            binding_file.write_bytes(m.encode(binding))
            (runtime/'runs/old/full.log').write_text('historical full log\n')
            (runtime/'schedules.json').write_text('{"schedules":[]}')
            (definitions/'planning/plan.json').write_text('{"steps":[{"id":"old"}]}')
            capabilities={'browser_mode':'cdp','browser_cdp_port':9222,'selected_global_secret_names':['EXAMPLE_TEST_SECRET'],'llm_config':{'mode':'provider','provider':'codex-cli'},'selected_servers':['example-app'],'selected_tools':['example-app:*']}
            (definitions/'workflow.json').write_bytes(m.encode({'id':'task','capabilities':capabilities}))
            (definitions/'variables').mkdir()
            presets=[{'name':'Account A','values':{'account':'first'}},{'name':'Account B','values':{'account':'second'}}]
            (definitions/'variables/variables.json').write_bytes(m.encode({'variables':[{'name':'account','type':'string','value':'first'}],'groups':presets}))
            (runtime/'functions.json').write_text('{"functions":[{"name":"second_account","input":{"input_preset":"Account B"}}]}')
            (code/'old-step/main.py').write_text('print("old source")\n')
            db = sqlite3.connect(runtime/'db/db.sqlite')
            db.execute('CREATE TABLE historical (id INTEGER PRIMARY KEY, result TEXT)')
            db.execute("INSERT INTO historical VALUES (7, 'kept')")
            db.commit(); db.close()
            before = hashes(runtime)
            changes, documents, report = m.plan(docs,state,True)
            self.assertEqual(report['binding_count'],1)
            self.assertEqual(report['bindings'][0]['runtime_root'],'workflows/task')
            self.assertNotIn('runtime_root',json.loads(binding_file.read_text()))
            backup = m.apply(changes,documents,state)
            self.assertTrue((Path(backup)/'ownership/crew-executables'/binding_file.name).exists())
            self.assertFalse((Path(backup)/'hashes.json').exists())
            self.assertEqual(m.plan(docs,state,True)[0],{})
            (source/'script.json').write_text('{"id":"task","name":"Task","outputs":["result.json"]}')
            (source/'main.py').write_text('print("new Python top")\n')
            # A useful Crew DB must be an explicit owner choice, never overwritten.
            (private/'db').mkdir()
            db = sqlite3.connect(private/'db/db.sqlite')
            db.execute('CREATE TABLE notes (text TEXT)')
            db.execute("INSERT INTO notes VALUES ('keep')")
            db.commit();db.close()
            with self.assertRaisesRegex(ValueError,'already contains rows'):
                m.plan(docs,state,False,'task',source)
            db = sqlite3.connect(private/'db/db.sqlite');db.execute('DELETE FROM notes');db.commit();db.close()
            source_definition=json.loads((source/'script.json').read_text())
            source_definition['capabilities']={'browser_mode':'extension'}
            (source/'script.json').write_bytes(m.encode(source_definition))
            with self.assertRaisesRegex(ValueError,'changes the saved browser_mode selection'):
                m.plan(docs,state,False,'task',source)
            source_definition['capabilities']={'browser_mode':'cdp'}
            (source/'script.json').write_bytes(m.encode(source_definition))
            changes,documents,report = m.plan(docs,state,False,'task',source)
            self.assertEqual(report['conversion']['database_rows'],{'historical':1})
            backup = Path(m.apply(changes,documents,state))
            self.assertEqual(hashes(runtime),before)
            self.assertEqual(hashes(backup/'runtime'),before)
            self.assertTrue((backup/'definitions/planning/plan.json').exists())
            self.assertTrue((definitions/'planning/plan.json').exists())
            self.assertEqual((code/'main.py').read_bytes(),(source/'main.py').read_bytes())
            self.assertEqual(json.loads((code/'script.json').read_text())['capabilities'],capabilities)
            self.assertEqual(json.loads((code/'script.json').read_text())['input_presets'],presets)
            self.assertIn('account',[v['name'] for v in json.loads((code/'script.json').read_text())['inputs']])
            self.assertEqual(json.loads(binding_file.read_text()),dict(binding,kind='script',runtime_root='workflows/task'))
            saved=json.loads(projects_file.read_text())['projects']['work/example']
            self.assertEqual(saved['database_root'],'workflows/task/db')
            self.assertEqual(m.database_rows(runtime/'db/db.sqlite'),{'historical':1})
            # A second conversion refuses instead of silently selecting another DB.
            binding['kind']='workflow';binding['runtime_root']='workflows/task'
            binding_file.write_bytes(m.encode(binding))
            saved['database_root']='workflows/another/db'
            projects['projects']['work/example']=saved;projects_file.write_bytes(m.encode(projects))
            with self.assertRaisesRegex(ValueError,'database_root=workflows/another/db'):
                m.plan(docs,state,False,'task',source)
            saved.pop('database_root');projects_file.write_bytes(m.encode(projects))
            binding['crew_root']='Crew/another';binding_file.write_bytes(m.encode(binding))
            with self.assertRaisesRegex(ValueError,'does not match its owning Crew'):
                m.plan(docs,state,False,'task',source)
            binding['crew_root']='Crew/example';binding['runtime_root']='../another';binding_file.write_bytes(m.encode(binding))
            with self.assertRaisesRegex(ValueError,'Invalid private automation runtime root'):
                m.plan(docs,state,False,'task',source)


if __name__ == '__main__':
    unittest.main()
