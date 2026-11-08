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
    def test_conversion_keeps_explicit_mail_binding_but_drops_summary_fields(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'script.json').write_text('{"id":"task","name":"Report"}')
            (root/'main.py').write_text('print("ready")\n')
            capabilities = {'notifications': {'gmail_connection_id':'selected-account',
                'block_recipients':['blocked@example.com'], 'run_summary_recipients':['old@example.com'],
                'run_summary_channels':['gmail']}}
            payload, definition, report = m.python_payload(root, 'task', capabilities, {})
            self.assertEqual(definition['capabilities'], {'gmail_connection_id':'selected-account',
                'gmail_blocked_recipients':['blocked@example.com']})
            self.assertEqual(report['carried_mail_binding'], definition['capabilities'])
            self.assertEqual(report['removed_notification_fields'], ['run_summary_channels','run_summary_recipients'])
            self.assertIn(b'selected-account', payload['script.json'])
            # Inspect/conversion must not mutate the original manifest snapshot.
            self.assertIn('gmail_connection_id', capabilities['notifications'])

    def test_explicit_entry_mapping_preserves_auth_timing_callers_and_unmapped_reminders(self):
        # PLAT-746-450: converting routing choices must not recreate webhooks,
        # activate held timers or rewrite their auth/caller/dependency records.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            definition = {'inputs': [{'name':'ACTION','type':'string','value':'review'}],
                          'input_presets':[{'name':'default','values':{'ACTION':'review'}}]}
            run = {'id':'kept','name':'Existing','enabled':False,'action':{'type':'run','step':'old-step','tab':'main'},
                   'input':{'input_preset':'default','routes':{'branch':'review'}},
                   'when':{'cron':'0 8 * * *','after':{'schedules':['previous']}},
                   'auth':{'encrypted_secret':'ciphertext-fixture'},'run_as':'owner','project':{'kind':'goal','id':'task'}}
            reminder = {'id':'reminder','enabled':False,'action':{'type':'message'},'input':{'messages':['Keep this reminder']}}
            unmapped = dict(run, id='unmapped')
            (root/'schedules.json').write_bytes(m.encode({'schedules':[run,reminder,unmapped]}))
            original = (root/'schedules.json').read_bytes()
            # Only the external validation boundary is a fixture. Runtime schema
            # and real webhook/Python validation are checked by the Go fixture.
            validator = root/'validator'
            validator.write_text('#!/usr/bin/env python3\nimport json,sys\nx=json.load(sys.stdin)\nassert x["functions"][0]["input"]["input_preset"]=="review"\nassert x["definitions"]["variables"][0]["name"]=="ACTION"\n')
            validator.chmod(0o700)
            mapping = {'input_presets':[{'name':'review','values':{'ACTION':'review'}}],
                       'schedules':{'kept':{'input_preset':'review','inputs':{'ACTION':'review'}}}}
            staged = {}
            read = lambda f, default: json.loads(f.read_text()) if f.exists() else default
            report = m.remap_entry_inputs(definition,staged,root,read,mapping,validator)
            self.assertEqual((root/'schedules.json').read_bytes(),original)
            entries = json.loads(staged[root/'schedules.json'])['schedules']
            self.assertEqual(entries[0],dict(run,action={'type':'run','tab':'main'},input={'input_preset':'review','inputs':{'ACTION':'review'}}))
            self.assertEqual(entries[1:], [reminder,unmapped])
            self.assertEqual(report['unmapped_entry_inputs'],[{'kind':'schedules','id':'unmapped','enabled':False}])
            self.assertEqual(len(definition['input_presets']),2)
            with self.assertRaisesRegex(ValueError,'existing entry ID'):
                m.remap_entry_inputs(definition,{},root,read,{'schedules':{'missing':{}}},validator)
            with self.assertRaisesRegex(ValueError,'message reminders unchanged'):
                m.remap_entry_inputs(definition,{},root,read,{'schedules':{'reminder':{}}},validator)
            with self.assertRaisesRegex(ValueError,'already exists with different values'):
                m.remap_entry_inputs(definition,{},root,read,{'input_presets':[{'name':'default','values':{'ACTION':'changed'}}]},validator)
            # The same mapper preserves the hook's encrypted auth and shared
            # caller rules while replacing only its old routing configuration.
            hook = dict(run,id='hook',input_mapping={'mode':'raw','payload':{'routes':{'branch':{'source':'kind','values':{'issue':'review'}}}}},caller={'id':'caller'})
            (root/'webhooks.json').write_bytes(m.encode({'webhooks':[hook]}))
            staged = {}
            report = m.remap_entry_inputs(definition,staged,root,read,{'webhooks':{'hook':{'input_preset':'review','allowed_variables':['ACTION'],'payload_mappings':{'inputs':{'ACTION':{'source':'kind','values':{'issue':'review'}}}}}}},validator)
            converted=json.loads(staged[root/'webhooks.json'])['webhooks'][0]
            for key in ('id','name','enabled','auth','caller','run_as','project','when'):
                self.assertEqual(converted[key],hook[key])
            self.assertEqual(converted['input_mapping']['payload'],{'inputs':{'ACTION':{'source':'kind','values':{'issue':'review'}}}})
            # Review4506403845/46: a preset-only update must retain raw input
            # mappings and submit the actual mapping to the server validator.
            (root/'webhooks.json').write_bytes(m.encode({'webhooks':[converted]}))
            validator.write_text('#!/usr/bin/env python3\nimport json,sys\nx=json.load(sys.stdin)\nf=x["functions"][0]\nassert f["input_mapping"]["payload"]["inputs"]["ACTION"]["values"]=={"issue":"review"}\nassert f["input_mapping"]["variables"]==["ACTION"]\n')
            staged={}
            m.remap_entry_inputs(definition,staged,root,read,{'webhooks':{'hook':{'input_preset':'review'}}},validator)
            preserved=json.loads(staged[root/'webhooks.json'])['webhooks'][0]
            self.assertEqual(preserved['input_mapping'],converted['input_mapping'])


    def test_shared_backup_root_keeps_each_workflow_and_refuses_readonly_snapshot_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            state, backup = root/'state', root/'backups'
            source = root/'first-source'
            source.mkdir()
            original = source/'db.sqlite'
            original.write_bytes(b'first database')
            original.chmod(0o444)
            cache = source/'.tmp'
            cache.mkdir()
            (cache/'git-askpass.sh').write_bytes(b'temporary')
            (cache/'git-askpass.sh').chmod(0)
            (cache/'dangling').symlink_to('missing')
            changed = state/'selected.json'
            first = Path(m.apply({changed:b'first'}, {'runtime':source}, state, backup, 'first'))
            source2 = root/'second-source'
            source2.mkdir()
            (source2/'db.sqlite').write_bytes(b'second database')
            second = Path(m.apply({changed:b'second'}, {'runtime':source2}, state, backup, 'second'))
            self.assertEqual((first/'runtime/db.sqlite').read_bytes(),b'first database')
            self.assertEqual(json.loads((first/'skipped.json').read_text()), ['runtime/.tmp'])
            self.assertFalse((first/'runtime/.tmp').exists())
            self.assertEqual((cache/'git-askpass.sh').stat().st_mode & 0o777, 0)
            self.assertTrue((cache/'dangling').is_symlink())
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
            projects['projects'].update({name:{'product':'work','owner_id':'owner','project_id':name,'folder':name}
                                         for name in ('linked', 'already')})
            projects_file.write_bytes(m.encode(projects))
            crew_manifest = {'id':'crew','workflow_context_paths':['Crew/already'], 'capabilities':{'selected_global_secret_names':['UNCHANGED']}}
            (private/'workflow.json').write_bytes(m.encode(crew_manifest))
            binding = {'id':'task','crew_id':'crew','crew_root':'Crew/example','workspace_path':'Workflow/kept-name','kind':'workflow'}
            binding_file = state/'ownership/crew-executables'/(hashlib.sha256(binding['workspace_path'].encode()).hexdigest()+'.json')
            binding_file.write_bytes(m.encode(binding))
            (runtime/'runs/old/full.log').write_text('historical full log\n')
            (runtime/'schedules.json').write_text('{"schedules":[]}')
            (definitions/'planning/plan.json').write_text('{"steps":[{"id":"old","write_access":{"knowledgebase":true},"validation_schema":{"required_files":["result.json"]}}]}')
            (definitions/'planning/step_config.json').write_bytes(m.encode({'steps':[{'id':'old','agent_configs':{'execution_llm':{'provider':'codex-cli','model_id':'example-model','options':{'reasoning_effort':'high'}}}}]}))
            capabilities={'browser_mode':'cdp','browser_cdp_port':9222,'selected_global_secret_names':['EXAMPLE_TEST_SECRET'],'llm_config':{'mode':'provider','provider':'codex-cli'},'selected_servers':['example-app'],'selected_tools':['example-app:*']}
            # A retired tool in carried selections is dropped (PLAT-746).
            (definitions/'workflow.json').write_bytes(m.encode({'id':'task','workflow_context_paths':['Crew/linked'], 'capabilities':dict(capabilities,selected_tools=['example-app:*','human_tools:record_summary'])}))
            (definitions/'variables').mkdir()
            presets=[{'name':'Account A','values':{'account':'first'}},{'name':'Account B','values':{'account':'second'}}]
            (definitions/'variables/variables.json').write_bytes(m.encode({'variables':[{'name':'account','type':'string','value':'first'}],'groups':presets}))
            (runtime/'functions.json').write_text('{"functions":[{"name":"second_account","input":{"input_preset":"Account B"}}]}')
            (code/'old-step/main.py').write_text('print("old source")\n')
            db = sqlite3.connect(runtime/'db/db.sqlite')
            db.execute('CREATE TABLE historical (id INTEGER PRIMARY KEY, result TEXT)')
            db.execute("INSERT INTO historical VALUES (7, 'kept')")
            db.execute('CREATE TABLE background_agent_log (id INTEGER PRIMARY KEY, message TEXT)')
            db.execute("INSERT INTO background_agent_log VALUES (1, 'existing target log')")
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
            db = sqlite3.connect(private/'db/db.sqlite');db.execute('DELETE FROM notes')
            # PLAT-746: testing creates platform log rows before conversion;
            # those must not turn an otherwise empty Crew into a business clash.
            db.execute('CREATE TABLE background_agent_log (id INTEGER PRIMARY KEY, message TEXT)')
            db.execute("INSERT INTO background_agent_log VALUES (1, 'kept log')")
            db.commit();db.close()
            crew_db_before = (private/'db/db.sqlite').read_bytes()
            source_definition=json.loads((source/'script.json').read_text())
            # Settings and presets come from the saved workflow, never the source.
            for name in ('capabilities','input_presets'):
                (source/'script.json').write_bytes(m.encode(dict(source_definition,**{name:{}})))
                with self.assertRaisesRegex(ValueError,'Remove '+name):
                    m.plan(docs,state,False,'task',source)
            (source/'script.json').write_bytes(m.encode(source_definition))
            # A missing existing target refuses inspect before any data writes.
            missing = projects['projects'].pop('linked')
            projects_file.write_bytes(m.encode(projects))
            with self.assertRaisesRegex(ValueError, 'existing Linked project'):
                m.plan(docs,state,False,'task',source)
            self.assertEqual(json.loads((private/'workflow.json').read_text()), crew_manifest)
            projects['projects']['linked'] = missing
            projects_file.write_bytes(m.encode(projects))
            changes,documents,report = m.plan(docs,state,False,'task',source)
            self.assertEqual(report['conversion']['carried_crew_links'], ['Crew/linked'])
            self.assertEqual(report['conversion']['database_rows'],{'historical':1,'background_agent_log':1})
            self.assertEqual(report['conversion']['removed_tools'],['human_tools:record_summary'])
            self.assertEqual(report['conversion']['knowledge_write_steps'],['old'])
            self.assertEqual(report['conversion']['output_check_steps'],['old'])
            self.assertEqual(report['conversion']['reasoning_steps'],[{'step':'old','reasoning':'high','model':'codex-cli/example-model','source':'planning/step_config.json'}])
            backup = Path(m.apply(changes,documents,state,identity="task", append_platform_rows=bool(report["conversion"]["appended_crew_platform_rows"])))
            self.assertEqual(json.loads((backup/'crew-links').read_text()), crew_manifest)
            self.assertEqual(json.loads((private/'workflow.json').read_text()), dict(crew_manifest, workflow_context_paths=['Crew/already','Crew/linked']))
            self.assertEqual({k:v for k,v in hashes(runtime).items() if k != 'db/db.sqlite'}, {k:v for k,v in before.items() if k != 'db/db.sqlite'})
            self.assertEqual(hashes(backup/'runtime'),before)
            self.assertEqual((private/'db/db.sqlite').read_bytes(), crew_db_before)
            self.assertEqual((backup/'crew-db/db.sqlite').read_bytes(), crew_db_before)
            self.assertEqual(report['conversion']['crew_database_rows']['background_agent_log'], 1)
            self.assertEqual(report['conversion']['appended_crew_platform_rows'], 1)
            self.assertTrue((backup/'definitions/planning/plan.json').exists())
            self.assertTrue((definitions/'planning/plan.json').exists())
            self.assertEqual((code/'main.py').read_bytes(),(source/'main.py').read_bytes())
            self.assertEqual(json.loads((code/'script.json').read_text())['capabilities'],capabilities)
            self.assertEqual(json.loads((code/'script.json').read_text())['secrets'], ['EXAMPLE_TEST_SECRET'])
            self.assertEqual(json.loads((code/'script.json').read_text())['input_presets'],presets)
            self.assertIn('account',[v['name'] for v in json.loads((code/'script.json').read_text())['inputs']])
            self.assertEqual(json.loads(binding_file.read_text()),dict(binding,kind='script',runtime_root='workflows/task'))
            saved=json.loads(projects_file.read_text())['projects']['work/example']
            self.assertEqual(saved['database_root'],'workflows/task/db')
            self.assertEqual(m.database_rows(runtime/'db/db.sqlite'),{'historical':1,'background_agent_log':2})
            db = sqlite3.connect(runtime/'db/db.sqlite')
            self.assertEqual(db.execute('SELECT id,message FROM background_agent_log ORDER BY id').fetchall(), [(1, 'existing target log'), (2, 'kept log')])
            db.close()
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
            self.assertEqual(sorted(retired['removed']),['planning/changelog/changelog-1.json','planning/plan.json','planning/revisions/plan-old.json','planning/step_config.json'])
            retire_backup=Path(m.apply_retirement(removals,docs_map,state,None,'task'))
            self.assertTrue((retire_backup/'definitions-planning/plan.json').exists())
            self.assertFalse((definitions/'planning').exists())
            self.assertTrue((runtime/'planning/revisions/plan-new.json').exists() and (runtime/'planning/revisions/plan-failed.json').exists())
            self.assertTrue((definitions/'workflow.json').exists() and (code/'main.py').exists() and (runtime/'runs/old/full.log').exists())
            # A second conversion refuses instead of silently selecting another DB.
            binding['kind']='workflow';binding['runtime_root']='workflows/task'
            binding_file.write_bytes(m.encode(binding))
            # A prior explicit --single-db choice already appended platform logs.
            repeated_changes, repeated_docs, repeated_report = m.plan(docs,state,False,'task',source)
            self.assertEqual(repeated_report['conversion']['appended_crew_platform_rows'], 0)
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


class SingleDatabase(unittest.TestCase):
    def test_points_at_the_only_database_with_rows_merges_or_refuses_on_clash(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp).resolve(); docs, state = base/'docs', base/'state'
            (state/'ownership/crew-executables').mkdir(parents=True)
            def make_db(path, table, rows, extra=''):
                path.parent.mkdir(parents=True, exist_ok=True)
                db = sqlite3.connect(path); db.execute(f'CREATE TABLE {table} (id INTEGER PRIMARY KEY, v TEXT{extra})')
                db.executemany(f'INSERT INTO {table} (v) VALUES (?)', [(str(i),) for i in range(rows)]); db.commit(); db.close()
            projects = {'projects': {}}
            for crew, flows in (('one', ['a']), ('merge', ['b']), ('clash', ['c']), ('logs', ['d']), ('drift', ['e']), ('crew-target', ['f'])):
                projects['projects']['work/'+crew] = {'product': 'work', 'project_id': crew, 'owner_id': 'owner', 'folder': crew}
                for flow in flows:
                    binding = {'id': flow, 'crew_id': crew, 'kind': 'workflow', 'runtime_root': 'workflows/'+flow}
                    (state/'ownership/crew-executables'/(flow+'.json')).write_bytes(m.encode(binding))
            (state/'ownership/projects.json').write_bytes(m.encode(projects))
            private = docs/'_system/crew-data'
            make_db(private/'one/db/db.sqlite', 'empty', 0)
            make_db(private/'one/workflows/a/db/db.sqlite', 'leads', 3)
            make_db(private/'merge/db/db.sqlite', 'notes', 1)
            make_db(private/'merge/workflows/b/db/db.sqlite', 'leads', 5)
            make_db(private/'clash/db/db.sqlite', 'leads', 1)
            make_db(private/'clash/workflows/c/db/db.sqlite', 'leads', 2)
            make_db(private/'crew-target/db/db.sqlite', 'notes', 5)
            make_db(private/'crew-target/workflows/f/db/db.sqlite', 'leads', 1)
            # Platform tables with identical columns are appended, with new row ids.
            make_db(private/'logs/db/db.sqlite', 'background_agent_log', 2)
            make_db(private/'logs/workflows/d/db/db.sqlite', 'background_agent_log', 3)
            # ...but a platform table whose columns differ still refuses.
            make_db(private/'drift/db/db.sqlite', 'background_agent_log', 1)
            make_db(private/'drift/workflows/e/db/db.sqlite', 'background_agent_log', 2, ', extra TEXT')
            merges, crews = m.single_db_plan(docs, state)
            actions = {c['crew']: c['action'] for c in crews}
            self.assertEqual(actions['work/one'], 'use a')
            self.assertTrue(actions['work/merge'].startswith('copy 1 table(s) into b'))
            self.assertEqual(actions['work/clash'], 'refused: tables with rows exist in more than one database: leads')
            self.assertEqual(actions['work/drift'], 'refused: tables with rows exist in more than one database: background_agent_log (platform table columns differ)')
            self.assertEqual({c['crew']: c.get('append_platform_rows') for c in crews}['work/logs'], {'background_agent_log': 2})
            backup = Path(m.apply_single_db(merges, docs, state, None))
            saved = json.loads((state/'ownership/projects.json').read_text())['projects']
            self.assertEqual(saved['work/one']['database_root'], 'workflows/a/db')
            self.assertEqual(saved['work/merge']['database_root'], 'workflows/b/db')
            self.assertNotIn('database_root', saved['work/clash'])
            self.assertEqual(m.database_rows(private/'merge/workflows/b/db/db.sqlite'), {'leads': 5, 'notes': 1})
            # Nothing deleted: the source database keeps its rows as history.
            self.assertEqual(m.database_rows(private/'merge/db/db.sqlite'), {'notes': 1})
            self.assertTrue((backup/'merge/db.sqlite').exists() and (backup/'projects.json').exists())
            self.assertEqual(m.database_rows(private/'logs/workflows/d/db/db.sqlite'), {'background_agent_log': 5})
            self.assertEqual(m.database_rows(private/'crew-target/db/db.sqlite'), {'notes': 5, 'leads': 1})
            # PLAT-746-456: retained source rows are frozen history. A repeat
            # inspect/apply must not append their platform events a second time.
            before = {str(p): m.database_rows(p) for p in private.rglob('db.sqlite')}
            again, crews = m.single_db_plan(docs, state)
            actions = {c['crew']: c['action'] for c in crews}
            self.assertEqual(actions['work/logs'], 'nothing: already uses d')
            self.assertEqual(actions['work/merge'], 'nothing: already uses b')
            self.assertEqual(actions['work/crew-target'], 'nothing: already uses crew')
            self.assertEqual(again, [])
            self.assertIsNone(m.apply_single_db(again, docs, state, None))
            self.assertEqual({str(p): m.database_rows(p) for p in private.rglob('db.sqlite')}, before)
            # A large historical source never takes precedence over a saved choice.
            db = sqlite3.connect(private/'logs/db/db.sqlite')
            db.executemany('INSERT INTO background_agent_log(v) VALUES (?)', [('old',)] * 20)
            db.commit(); db.close()
            again, crews = m.single_db_plan(docs, state)
            self.assertEqual(again, [])
            self.assertEqual(next(c['action'] for c in crews if c['crew'] == 'work/logs'), 'nothing: already uses d')


if __name__ == '__main__':
    unittest.main()
