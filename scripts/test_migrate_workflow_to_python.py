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
    def test_shared_backup_root_keeps_each_workflow_and_refuses_readonly_snapshot_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            state, backup = root/'state', root/'backups'
            source = root/'first-source'
            source.mkdir()
            original = source/'db.sqlite'
            original.write_bytes(b'first database')
            original.chmod(0o444)
            changed = state/'selected.json'
            first = Path(m.apply({changed:b'first'}, {'runtime':source}, state, backup, 'first'))
            source2 = root/'second-source'
            source2.mkdir()
            (source2/'db.sqlite').write_bytes(b'second database')
            second = Path(m.apply({changed:b'second'}, {'runtime':source2}, state, backup, 'second'))
            self.assertEqual((first/'runtime/db.sqlite').read_bytes(),b'first database')
            self.assertEqual((second/'runtime/db.sqlite').read_bytes(),b'second database')
            with self.assertRaisesRegex(ValueError,'already contains a snapshot'):
                m.apply({changed:b'overwritten'}, {'runtime':source2}, state, backup, 'first')
            self.assertEqual(changed.read_bytes(),b'second')
            self.assertEqual((first/'runtime/db.sqlite').read_bytes(),b'first database')

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
            (definitions/'planning/plan.json').write_text('{"steps":[{"id":"old","write_access":{"knowledgebase":true}}]}')
            capabilities={'browser_mode':'cdp','browser_cdp_port':9222,'selected_global_secret_names':['EXAMPLE_TEST_SECRET'],'llm_config':{'mode':'provider','provider':'codex-cli'},'selected_servers':['example-app'],'selected_tools':['example-app:*']}
            # A retired tool in carried selections is dropped (PLAT-746).
            (definitions/'workflow.json').write_bytes(m.encode({'id':'task','capabilities':dict(capabilities,selected_tools=['example-app:*','human_tools:record_summary'])}))
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
            # Settings and presets come from the saved workflow, never the source.
            for name in ('capabilities','input_presets'):
                (source/'script.json').write_bytes(m.encode(dict(source_definition,**{name:{}})))
                with self.assertRaisesRegex(ValueError,'Remove '+name):
                    m.plan(docs,state,False,'task',source)
            (source/'script.json').write_bytes(m.encode(source_definition))
            changes,documents,report = m.plan(docs,state,False,'task',source)
            self.assertEqual(report['conversion']['database_rows'],{'historical':1})
            self.assertEqual(report['conversion']['removed_tools'],['human_tools:record_summary'])
            self.assertEqual(report['conversion']['knowledge_write_steps'],['old'])
            backup = Path(m.apply(changes,documents,state,identity="task"))
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
            # Retiring the old plan waits for a completed converted run, keeps that
            # run's plan revision, and leaves workflow.json, code and runs alone.
            (runtime/'planning/revisions').mkdir(parents=True);(runtime/'planning/changelog').mkdir()
            (runtime/'planning/revisions/plan-new.json').write_text('{}');(runtime/'planning/revisions/plan-old.json').write_text('{}')
            (runtime/'planning/changelog/changelog-1.json').write_text('{}')
            with self.assertRaisesRegex(ValueError,'successfully at least once'):
                m.retire_old_plan(docs,state,'task')
            (runtime/'runs/run-1').mkdir(parents=True)
            (runtime/'runs/run-1/run_metadata.json').write_bytes(m.encode({'status':'completed','required_step_ids':['task'],'plan_revision':'plan-new'}))
            # A failed run keeps the revision it recorded for inspection and resume.
            (runtime/'runs/run-2').mkdir()
            (runtime/'runs/run-2/run_metadata.json').write_bytes(m.encode({'status':'failed','required_step_ids':['task'],'plan_revision':'plan-failed'}))
            (runtime/'planning/revisions/plan-failed.json').write_text('{}')
            removals,docs_map,retired=m.retire_old_plan(docs,state,'task')
            self.assertEqual(retired['completed_converted_runs'],['run-1'])
            self.assertEqual(sorted(retired['removed']),['planning/changelog/changelog-1.json','planning/plan.json','planning/revisions/plan-old.json'])
            retire_backup=Path(m.apply_retirement(removals,docs_map,state,None,'task'))
            self.assertTrue((retire_backup/'definitions-planning/plan.json').exists())
            self.assertFalse((definitions/'planning').exists())
            self.assertTrue((runtime/'planning/revisions/plan-new.json').exists() and (runtime/'planning/revisions/plan-failed.json').exists())
            self.assertTrue((definitions/'workflow.json').exists() and (code/'main.py').exists() and (runtime/'runs/old/full.log').exists())
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
