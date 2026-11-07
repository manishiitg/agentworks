"""PLAT-746-265: one-Goal conversion preserves history and cannot widen access."""
from contextlib import closing
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest

spec=importlib.util.spec_from_file_location('goal_conversion',Path(__file__).with_name('migrate-goal-to-crew.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)


def write(p,v):
    p.parent.mkdir(parents=True,exist_ok=True)
    p.write_text(json.dumps(v))


def hashes(root):
    return {str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in root.rglob('*') if p.is_file()}


class Migration(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
        self.base=Path(self.tmp.name).resolve();self.docs=self.base/'docs';self.state=self.base/'state'
        self.source=self.docs/'Workflow/old-goal';self.private=self.docs/'_system/crew-data/crew'
        self.runtime=self.private/'workflows/old-goal'
        write(self.state/'ownership/workflows.json',{'Workflow/old-goal':'owner'})
        write(self.state/'ownership/projects.json',{'projects':{'work/target':{'product':'work','folder':'target','owner_id':'owner','project_id':'crew','private':True,'run_people':['runner']}}})
        (self.docs/'Crew/target').mkdir(parents=True)
        write(self.private/'product.json',{'id':'crew','product':'work','main_chat_key':'crew:chat:existing'})
        write(self.source/'workflow.json',{'id':'old-goal','label':'Revenue goal','created_by':'owner','access':{'owners':['owner'],'readers':['runner']},'schedules':[], 'capabilities':{'selected_secrets':['api-name']}})
        (self.source/'soul').mkdir(parents=True)
        (self.source/'soul/soul.md').write_text('## Objective\nGrow qualified revenue.\n\n## Success Criteria\nVerified revenue.\n')
        write(self.source/'planning/plan.json',{'steps':[{'id':'one','type':'scripted'}]})
        write(self.source/'variables/variables.json',{'variables':[],'groups':[{'name':'default','values':{},'enabled':True}]})
        write(self.source/'code/one/main.py',{'source':'original'})
        # PLAT-746-336: valid bridge API names are not direct tracking-table SQL.
        (self.source/'code/one/main.py').write_text("bridge('record_goal_observations', {'observations': []})\n")
        write(self.source/'runs/iteration-1/run_metadata.json',{'run_id':'original-run'})
        write(self.source/'skills/example/SKILL.md',{'untouched':'skill'})
        write(self.source/'knowledgebase/example.json',{'untouched':'knowledge'})
        write(self.source/'db/reports/index.html',{'report':'original business report'})
        write(self.docs/'Workflow/unrelated/workflow.json',{'id':'unrelated'})
        self.key='old-goal:chat:owner-'+hashlib.sha256(b'owner').hexdigest()[:16]
        record={'profile_id':'agentworks','conversation_key':self.key,'conversation_id':'conversation-original','session_id':'session-original','workspace_path':'Workflow/old-goal','resource_id':'old-goal'}
        write(self.docs/'_users/owner/chat_history/product-conversations.json',{'entries':{'agentworks/'+self.key:record},'agents':{'agentworks/'+self.key:{'name':'Revenue Builder'}}})
        self.oldpath='Workflow/old-goal/builder/conversation/users/owner/2026-11-04/session-session-original-conversation.json'
        write(self.docs/self.oldpath,{'session_id':'session-original','user_id':'owner','workspace_path':'Workflow/old-goal','conversation_history':[{'role':'user','content':'Do not rewrite Workflow/old-goal inside messages'}]})
        write(self.docs/'_users/owner/chat_history/index.json',{'entries':{self.oldpath:{'session':{'session_id':'session-original','workspace_path':'Workflow/old-goal','conversation_path':self.oldpath},'conversation_path':self.oldpath}}})
        # Real personal history also contains delivery receipts, with owner rather
        # than transcript user_id. These are historical receipts, not chat archives.
        self.receipt=self.docs/'_users/owner/chat_history/submissions/delivery.json'
        write(self.receipt,{'owner':'owner','project':'old-goal','session_id':'session-original','state':'delivered'})
        for filename,field in [('schedules.json','schedules'),('functions.json','functions'),('webhooks.json','webhooks')]:
            write(self.source/filename,{field:[{'id':field+'-original','name':field,'enabled':False,'run_as':'owner','project':{'kind':'goal','id':'old-goal'},'action':{'type':'run','tab':self.key},'input':{'groups':['default']},'auth':{'encrypted_secret':'opaque-not-printed'}}]})
        with closing(sqlite3.connect(self.source/'db/db.sqlite')) as db, db:
            db.execute('CREATE TABLE business_data(value TEXT)');db.execute("INSERT INTO business_data VALUES ('unchanged')")
            db.execute('CREATE TABLE workflow_goal_metrics(metric_id TEXT PRIMARY KEY, definition_json TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1)')
            db.execute('INSERT INTO workflow_goal_metrics VALUES (?,?,1)',('revenue',json.dumps({'id':'revenue','role':'primary','unit':'USD','target':20})))
            db.execute('CREATE TABLE goal_observations(observation_id TEXT PRIMARY KEY,criterion_id TEXT,metric TEXT,run_id TEXT,route TEXT,environment TEXT,value REAL,status TEXT,unit TEXT,observed_at TEXT,window_start TEXT,window_end TEXT,evidence_json TEXT,recorded_at TEXT)')
            db.execute('CREATE INDEX idx_pulse_goal_observations_criterion ON goal_observations(criterion_id, observed_at)')
            db.execute('INSERT INTO goal_observations VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)',('original-observation','earned','revenue','source-run','','',12,'','USD','2026-11-01T00:00:00Z','','','["source report"]','2026-11-01T00:00:00Z'))

        # Existing canonical chat journal must remain untouched by conversion.
        with closing(sqlite3.connect(self.state/'structured-chat-events-v2.sqlite')) as db,db:
            db.execute('CREATE TABLE structured_chat_sessions(session_id TEXT,owner_id TEXT)')
            db.execute('CREATE TABLE structured_chat_sequences(session_id TEXT,last_sequence INTEGER)')
            db.execute("INSERT INTO structured_chat_sessions VALUES ('session-original','owner')")
            db.execute("INSERT INTO structured_chat_sequences VALUES ('session-original',2)")

    def conversion(self):return m.Conversion(self.docs,self.state,'old-goal','crew')

    def test_retired_measurements_remain_unchanged_without_creating_crew_goals(self):
        # PLAT-746-364: preserve historical data, not the retired Goal concept.
        # Existing steps still read ordinary helper files under this path.
        helper = self.source/'learnings/_global/helper.py'
        helper.parent.mkdir(parents=True)
        helper.write_text('from pathlib import Path\ndef read_note(): return Path(__file__).with_name("note.txt").read_text()\n')
        helper.with_name('note.txt').write_text('preserved guidance')
        before = (self.source/'db/db.sqlite').read_bytes()
        conversion = m.Conversion(self.docs,self.state,'old-goal','crew',keep_measurements=True)
        self.assertEqual(conversion.report()['counts']['goals'], 0)
        conversion.apply()
        self.assertEqual((self.runtime/'db/db.sqlite').read_bytes(), before)
        self.assertFalse((self.private/'db/db.sqlite').exists())
        moved = self.runtime/'learnings/_global/helper.py'
        spec = importlib.util.spec_from_file_location('retained_helper', moved)
        module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
        self.assertEqual(module.read_note(), 'preserved guidance')
        with closing(sqlite3.connect(self.runtime/'db/db.sqlite')) as db:
            self.assertEqual(db.execute('SELECT COUNT(*) FROM workflow_goal_metrics').fetchone()[0], 1)
            self.assertEqual(db.execute('SELECT run_id,evidence_json FROM goal_observations').fetchone(), ('source-run','["source report"]'))
            self.assertIsNotNone(db.execute("SELECT 1 FROM sqlite_master WHERE name='idx_pulse_goal_observations_criterion'").fetchone())

    def test_legacy_function_groups_convert_before_contract_validation(self):
        # PR498: runtime validation rejects groups, so validate only after the
        # one-Goal converter has produced the final preset/function envelope.
        binary = os.environ.get('AGENTWORKS_TEST_AGENT_BINARY')
        if not binary:
            self.skipTest('set AGENTWORKS_TEST_AGENT_BINARY to the isolated refactor build')
        functions = m.read(self.source/'functions.json')
        functions['functions'][0]['input_schema'] = {'type': 'object', 'properties': {'count': {'type': 'integer', 'minimum': 1}}, 'required': ['count']}
        write(self.source/'functions.json', functions)
        definitions = {'variables': [{'name': 'count', 'value': 'bad'}], 'groups': [{'name': 'default', 'values': {}}]}
        write(self.source/'variables/variables.json', definitions)
        before = hashes(self.base)
        with self.assertRaisesRegex(ValueError, 'Input conversion needs an author decision'):
            m.Conversion(self.docs, self.state, 'old-goal', 'crew', binary).report()
        self.assertEqual(hashes(self.base), before)
        definitions['variables'][0]['value'] = '2'
        write(self.source/'variables/variables.json', definitions)
        conversion = m.Conversion(self.docs, self.state, 'old-goal', 'crew', binary)
        self.assertEqual(conversion.report()['status'], 'ready')
        conversion.apply()
        fn = m.read(self.runtime/'functions.json')['functions'][0]
        self.assertEqual(fn['inputs'], ['count'])
        self.assertEqual(fn['input'], {'input_preset': 'default'})
        self.assertNotIn('input_schema', fn)

    def test_one_goal_preserves_history_ids_other_goal_and_private_chats(self):
        registry_path = self.docs/'_users/owner/chat_history/product-conversations.json'
        registry = m.read(registry_path)
        registry['entries']['work/crew:chat:existing'] = {'profile_id':'work','conversation_key':'crew:chat:existing','session_id':'destination-session','resource_id':'crew','workspace_path':'Crew/target'}
        # A second imported chat is independent; only the owner's primary Builder becomes Main.
        extra = dict(registry['entries']['agentworks/'+self.key], conversation_key='old-goal:chat:research', session_id='research-session')
        registry['entries']['agentworks/old-goal:chat:research'] = extra
        write(registry_path, registry)

        # History indexes use archive paths, never canonical Builder tab keys.
        # Two rows must not trigger primary-chat selection in this second pass.
        index_path = self.docs/'_users/owner/chat_history/index.json'
        index = m.read(index_path)
        first = next(iter(index['entries'].values()))
        first['workspace_path'] = 'Workflow/old-goal'
        index['entries']['other-archive.json'] = {'workspace_path':'Workflow/old-goal', 'session':{'session_id':'research-session','workspace_path':'Workflow/old-goal'}}
        write(index_path, index)

        before=hashes(self.base);c=self.conversion()
        self.assertEqual(c.report()['status'],'ready');self.assertEqual(hashes(self.base),before)
        result=c.apply();self.assertTrue(Path(result['backup'],'files.tar.gz').exists())
        self.assertFalse((self.source/'workflow.json').exists())
        self.assertTrue((self.docs/'Crew/target/.agentworks/workflows/old-goal/workflow.json').exists())
        self.assertEqual((self.runtime/'runs/iteration-1/run_metadata.json').read_text(),'{"run_id": "original-run"}')
        after=hashes(self.base)
        for name in ['docs/Workflow/unrelated/workflow.json',str(self.receipt.relative_to(self.base))]:
            self.assertEqual(before[name],after[name])
        for name in ['skills/example/SKILL.md','knowledgebase/example.json','db/reports/index.html']:
            self.assertEqual(before['docs/Workflow/old-goal/'+name],after[str((self.runtime/name).relative_to(self.base))])
        self.assertFalse(self.source.exists())
        with closing(sqlite3.connect(self.private/'db/db.sqlite')) as db, db:
            self.assertEqual(db.execute('SELECT observation_id,metric,recorded_at FROM goal_observations').fetchall(),[('original-observation','revenue','2026-11-01T00:00:00Z')])
            self.assertEqual(db.execute('SELECT run_id FROM goal_observations').fetchone()[0],'old-goal:source-run')
            self.assertEqual(json.loads(db.execute('SELECT definition_json FROM crew_goals').fetchone()[0])['status'],'active')
            self.assertEqual(json.loads(db.execute('SELECT definition_json FROM crew_goals').fetchone()[0])['outcome'], 'Grow qualified revenue.')
            self.assertEqual([row[2] for row in db.execute('PRAGMA index_info(idx_pulse_goal_observations_criterion)')],['criterion_id','observed_at'])
        with closing(sqlite3.connect(self.runtime/'db/db.sqlite')) as db, db:
            self.assertEqual(db.execute('SELECT * FROM business_data').fetchall(),[('unchanged',)])
            self.assertFalse(db.execute("SELECT 1 FROM sqlite_master WHERE name='goal_observations'").fetchall())
        for filename,field in [('schedules.json','schedules'),('functions.json','functions'),('webhooks.json','webhooks')]:
            entry=m.read(self.runtime/filename)[field][0]
            self.assertFalse(entry['enabled']);self.assertTrue(entry['action']['tab'].startswith('imported-'))
            self.assertEqual(entry['input'],{'input_preset':'default'})
            self.assertEqual(entry['auth']['encrypted_secret'],'opaque-not-printed')
        transcript=next((self.private/'builder/conversation').rglob('*-conversation.json'))
        self.assertEqual(m.read(transcript)['conversation_history'][0]['content'],'Do not rewrite Workflow/old-goal inside messages')
        self.assertEqual(m.read(transcript)['user_id'],'owner')
        registry=m.read(self.docs/'_users/owner/chat_history/product-conversations.json')
        main_key = m.read(self.private/'product.json')['main_chat_key']
        row = registry['entries']['work/'+main_key]
        self.assertEqual(registry['agents']['work/'+main_key]['name'], 'Main')
        self.assertEqual([r['session_id'] for r in registry['previous']['work/'+main_key]], ['destination-session'])
        self.assertEqual(len(registry['entries']), 2)
        self.assertTrue(any(r['session_id'] == 'research-session' for r in registry['entries'].values()))
        self.assertEqual(row['session_id'],'session-original');self.assertEqual(row['resource_id'],'crew')
        index=m.read(self.docs/'_users/owner/chat_history/index.json')
        item=next(row for row in index['entries'].values() if row['session']['session_id']=='session-original')
        self.assertTrue(item['session']['conversation_path'].startswith('Crew/target/'))
        self.assertEqual(self.conversion().report()['status'],'already migrated')
        self.conversion().apply();self.assertEqual(after,hashes(self.base))

    def test_unregistered_owner_history_keeps_bytes_without_creating_tabs(self):
        # PLAT-746-364: real older archives have no surviving registry entry.
        path = Path('builder/conversation/users/owner/2026-06-02/session-old-conversation.json')
        archive = {'session_id':'old-session','user_id':'owner',
                   'conversation_history':[{'role':'user','content':'Keep the old conversation.'}]}
        write(self.source/path, archive)
        original = (self.source/path).read_bytes()
        before = hashes(self.base)
        conversion = self.conversion()
        self.assertEqual(conversion.report()['counts']['unregistered_chat_archives'], 1)
        self.assertEqual(hashes(self.base), before)
        conversion.apply()
        self.assertEqual((self.runtime/path).read_bytes(), original)
        registry = m.read(self.docs/'_users/owner/chat_history/product-conversations.json')
        self.assertEqual(len(registry['entries']), 1)
        self.assertNotIn('old-session', str(registry))

    def test_unregistered_other_person_history_cannot_move_into_owner_records(self):
        # An archive's age never grants access to another person's private chat.
        write(self.source/'builder/conversation/users/other/2026-06-02/session-old-conversation.json',
              {'session_id':'other-session','user_id':'other','conversation_history':[]})
        before = hashes(self.base)
        with self.assertRaisesRegex(ValueError, 'private owner mapping'):
            self.conversion()
        self.assertEqual(hashes(self.base), before)

    def test_finish_runtime_preserves_authored_files_and_references_without_recreating_old_folder(self):
        # PLAT-746-360: a prior source-only migration left native helpers
        # outside the runtime grant list. Finish only that workflow's records.
        self.conversion().apply()
        self.runtime.rename(self.source)
        write(self.source/'learnings/retired.json', {'keep_in_backup':True})
        write(self.source/'runs/history/run.json', {'output_path':str(self.source/'runs/history/out.json'), 'command':'cat '+str(self.source/'old.txt')})
        with closing(sqlite3.connect(self.source/'db/db.sqlite')) as db, db:
            db.execute('CREATE TABLE file_index(id TEXT PRIMARY KEY,path TEXT,metadata TEXT) WITHOUT ROWID')
            db.execute('INSERT INTO file_index VALUES (?,?,?)',('stable-id',str(self.source/'db/assets/file.txt'),json.dumps({'path':str(self.source/'runs/history/out.json')})))
            db.execute('CREATE TABLE historical_result(id INTEGER PRIMARY KEY,result TEXT,path TEXT)')
            db.execute("INSERT INTO historical_result VALUES (1,CAST(x'FF' AS TEXT),?)",(str(self.source/'runs/history/out.json'),))
        # A stopped/crashed SQLite writer can leave committed rows in WAL.
        # Copy every sidecar and prove those rows survive opening the new DB.
        cost_db=self.source/'costs/costs.sqlite'
        cost_db.parent.mkdir()
        subprocess.run([sys.executable,'-c',"import sqlite3,os,sys; db=sqlite3.connect(sys.argv[1]); db.execute('PRAGMA journal_mode=WAL'); db.execute('CREATE TABLE cost_events(id INTEGER PRIMARY KEY,value REAL)'); db.execute('INSERT INTO cost_events VALUES (1,1.25)'); db.commit(); os._exit(0)",str(cost_db)],check=True)
        self.assertTrue(Path(str(cost_db)+'-wal').exists())
        authored=hashes(self.docs/'Crew/target')
        other=hashes(self.docs/'Workflow/unrelated')
        conversion=m.RuntimeRelocation(self.docs,self.state,'old-goal','crew')
        self.assertEqual(conversion.report()['status'],'ready')
        result=conversion.apply()
        self.assertFalse(self.source.exists())
        self.assertEqual(m.read(self.runtime/'learnings/retired.json'), {'keep_in_backup':True})
        self.assertEqual(authored,hashes(self.docs/'Crew/target'))
        self.assertEqual(other,hashes(self.docs/'Workflow/unrelated'))
        evidence=m.read(self.runtime/'runs/history/run.json')
        self.assertEqual(evidence['output_path'],str(self.runtime/'runs/history/out.json'))
        self.assertEqual(evidence['command'],'cat '+str(self.source/'old.txt'))
        with closing(sqlite3.connect(self.runtime/'db/db.sqlite')) as db:
            key,path,metadata=db.execute('SELECT * FROM file_index').fetchone()
            self.assertEqual(key,'stable-id')
            self.assertEqual(path,str(self.runtime/'db/assets/file.txt'))
            self.assertEqual(json.loads(metadata)['path'],str(self.runtime/'runs/history/out.json'))
            self.assertEqual(db.execute('SELECT hex(result),path FROM historical_result').fetchone(),('FF',str(self.runtime/'runs/history/out.json')))
        with closing(sqlite3.connect(self.runtime/'costs/costs.sqlite')) as db:
            self.assertEqual(db.execute('SELECT * FROM cost_events').fetchall(),[(1,1.25)])
        backup=Path(result['backup'])
        self.assertIn('source/learnings/retired.json',m.read(backup/'sha256.json'))
        self.assertIn('source/costs/costs.sqlite-wal',m.read(backup/'sha256.json'))
        self.assertEqual(m.RuntimeRelocation(self.docs,self.state,'old-goal','crew').apply()['status'],'already migrated')
        self.assertFalse(self.source.exists())

    def test_moved_code_guidance_uses_runtime_anchor_and_reports_embedded_python(self):
        # PLAT-746-358: migration moved the script but kept its old filename
        # in agent instructions, so a real native step could not run it.
        write(self.source/'planning/plan.json', {'steps':[
            {'id':'one','type':'scripted','description':'Run `python3 code/one/main.py`. See https://example.com/code/one/main.py.', 'items':[{'message':'Read `code/one/main.py`.'}]},
            {'id':'two','type':'agent','description':"Use Path('code/one/main.py').read_text()"}]})
        write(self.source/'planning/step_config.json', {'one':{'command':'python3 code/one/main.py'}})
        before_script = (self.source/'code/one/main.py').read_bytes()
        conversion = self.conversion()
        pending = conversion.report()['code_path_manual_fixes']
        self.assertEqual(len(pending), 1)
        self.assertEqual(pending[0]['paths'], ['code/one/main.py'])
        conversion.apply()
        target = self.docs/'Crew/target/.agentworks'
        plan = m.read(target/'workflows/old-goal/planning/plan.json')
        self.assertEqual(plan['steps'][0]['description'], 'Run `python3 $WORKFLOW_CODE_ROOT/one/main.py`. See https://example.com/code/one/main.py.')
        self.assertEqual(plan['steps'][0]['items'][0]['message'], 'Read `$WORKFLOW_CODE_ROOT/one/main.py`.')
        self.assertEqual(plan['steps'][1]['description'], "Use Path('code/one/main.py').read_text()")
        self.assertEqual(m.read(target/'workflows/old-goal/planning/step_config.json')['one']['command'], 'python3 $WORKFLOW_CODE_ROOT/one/main.py')
        self.assertEqual((target/'code/old-goal/one/main.py').read_bytes(), before_script)
        self.assertEqual(m.read(self.runtime/'runs/iteration-1/run_metadata.json'), {'run_id':'original-run'})

    def test_refuses_access_expansion_collision_and_escaping_source_without_writes(self):
        registry=self.state/'ownership/projects.json';original=m.read(registry)
        changed=m.read(registry);changed['projects']['work/target']['private']=False;write(registry,changed)
        before=hashes(self.base)
        with self.assertRaisesRegex(ValueError,'sharing audience'):self.conversion()
        self.assertEqual(before,hashes(self.base));write(registry,original)
        team_file=self.state/'team-folders/crew/team.json'
        write(team_file,{'project_id':'crew','owner_id':'owner','on':True,'people':[{'user_id':'runner','invited':False}]})
        before=hashes(self.base)
        with self.assertRaisesRegex(ValueError,'joined Team members'):self.conversion()
        self.assertEqual(before,hashes(self.base))
        # An invitation alone grants no access; neither does a switched-off Team.
        for on,invited in [(True,True),(False,False)]:
            write(team_file,{'project_id':'crew','owner_id':'owner','on':on,'people':[{'user_id':'runner','invited':invited}]})
            self.assertEqual(self.conversion().report()['status'],'ready')
        team_file.unlink()
        private_chat=self.docs/'_users/owner/chat_history/2026-11-04/session-original-conversation.json'
        write(private_chat,{'session_id':'session-original','user_id':'another-person'})
        before=hashes(self.base)
        with self.assertRaisesRegex(ValueError,'conflicting ownership'):self.conversion()
        self.assertEqual(before,hashes(self.base));private_chat.unlink()
        destination=self.docs/'Crew/target/.agentworks/workflows/old-goal';destination.mkdir(parents=True)
        with self.assertRaisesRegex(ValueError,'already has source'):self.conversion()
        destination.rmdir()
        (self.source/'code/escape').symlink_to(self.base)
        with self.assertRaisesRegex(ValueError,'symlink'):self.conversion()


    def test_second_workflow_refused_without_changing_first(self):
        first=self.conversion().apply()
        source=self.docs/'Workflow/second-folder'
        write(source/'workflow.json',{'id':'second-id','label':'Reporting','access':{'owners':['owner'],'readers':['runner']}})
        owners=m.read(self.state/'ownership/workflows.json');owners['Workflow/second-folder']='owner';write(self.state/'ownership/workflows.json',owners)
        before = hashes(self.base)
        with self.assertRaisesRegex(ValueError, 'already has a workflow'):
            m.Conversion(self.docs,self.state,'second-folder','crew').apply()
        self.assertEqual(hashes(self.base), before)

    def test_legacy_batch_and_raw_database_scripts_report_before_any_writes(self):
        file=self.source/'schedules.json';doc=m.read(file);doc['schedules'][0]['input']={'groups':['one','two']};write(file,doc)
        before=hashes(self.base)
        with self.assertRaisesRegex(ValueError,'batch schedules'):self.conversion()
        self.assertEqual(before,hashes(self.base))
        doc['schedules'][0]['input']={};write(file,doc)
        (self.source/'code/one/main.py').write_text("import sqlite3, os\nsqlite3.connect(os.environ['DB_PATH'])\n")
        before=hashes(self.base)
        with self.assertRaisesRegex(ValueError,'agentworks_db'):self.conversion()
        self.assertEqual(before,hashes(self.base))
        (self.source/'code/one/main.py').write_text("sql = 'SELECT * FROM GOAL_OBSERVATIONS'\n")
        with self.assertRaisesRegex(ValueError,'direct measurement-table'):self.conversion()
        # PLAT-746-336: a real migrated helper looked for assets beside moved code.
        (self.source/'code/one/main.py').write_text("root = Path(os.environ['WORKFLOW_CODE_ROOT']).resolve().parent\n")
        before=hashes(self.base)
        with self.assertRaisesRegex(ValueError,'Code moves independently'):self.conversion()
        self.assertEqual(before,hashes(self.base))
        (self.source/'code/one/main.py').write_text('# no direct database use\n')
        with closing(sqlite3.connect(self.source/'db/db.sqlite')) as db,db:
            db.execute('CREATE VIEW measurement_summary AS SELECT * FROM goal_observations')
        before=hashes(self.base)
        with self.assertRaisesRegex(ValueError,'database dependencies'):self.conversion()
        self.assertEqual(before,hashes(self.base))


if __name__=='__main__':unittest.main()
