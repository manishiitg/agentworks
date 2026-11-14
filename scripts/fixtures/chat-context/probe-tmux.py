# Exercise the same passthrough bytes through an isolated tmux, not the app.
import os,pty,select,time,pathlib,tempfile,subprocess,re,base64,shutil,shlex
root=pathlib.Path(__file__).resolve().parents[3]
tmux=shutil.which('tmux'); assert tmux
hook=(root/'workspace/handlers/interactive_shell_context.bash').read_text().replace('__AW_GENERATION__','probe')
with tempfile.TemporaryDirectory(prefix='tmux-context-',dir=root.parent) as work:
 socket=str(pathlib.Path(work).resolve()/'tmux.sock')
 rc=pathlib.Path(work)/'rc';rc.write_text('PROMPT_COMMAND='+shlex.quote("PS1='PROBE> '; "+hook)+'\n')
 env={**os.environ,'TERM':'xterm-256color','HISTFILE':'/dev/null','BASH_SILENCE_DEPRECATION_WARNING':'1','HISTCONTROL':'','HISTIGNORE':''}
 base=[tmux,'-f','/dev/null','-S',socket]
 subprocess.run(base+['start-server',';','set-option','-g','allow-passthrough','on',';','set-option','-g','status','off',';','new-session','-d','-s','probe',f"/bin/bash --noprofile --rcfile '{rc}' -i"],env=env,check=True)
 pid,fd=pty.fork()
 if not pid: os.execve(tmux,base+['attach','-t','probe'],env)
 def read():
  out=b'';end=time.monotonic()+3
  while time.monotonic()<end:
   if select.select([fd],[],[],.2)[0]:
    try:out+=os.read(fd,65536)
    except OSError:break
   elif out:break
  return out
 try:
  read();os.write(fd,b'printf TMUX-PROOF | cat\n');out=read()
  starts=re.findall(rb'6973;C;probe-\d+;([^\x07]*)\x07',out)
  assert [base64.b64decode(x).decode() for x in starts]==['printf TMUX-PROOF | cat'],out
  assert b'6973;D;probe-1;0\x07' in out,out
  print('PASS: real tmux passthrough retains command and finish markers around pipeline output')
 finally:
  subprocess.run(base+['kill-server'],check=False,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
  read();os.close(fd);os.waitpid(pid,0)
