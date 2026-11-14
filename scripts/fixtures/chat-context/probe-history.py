# Isolated real Bash regression for managed terminal provenance. No app/build.
import os,pty,select,time,re,base64,tempfile,pathlib,signal,shlex
root=pathlib.Path(__file__).resolve().parents[3]
hook=(root/'workspace/handlers/interactive_shell_context.bash').read_text().replace('__AW_GENERATION__','probe')
# Keep artifacts within the owned worktree, never the owner's shell startup.
with tempfile.TemporaryDirectory(prefix='shell-context-',dir=pathlib.Path(__file__).parent) as work:
 rc=pathlib.Path(work)/'rc'; rc.write_text('PROMPT_COMMAND='+shlex.quote("PS1='PROBE> '; "+hook)+'\n')
 pid,fd=pty.fork()
 if not pid:
  os.environ.update(HISTFILE='/dev/null',BASH_SILENCE_DEPRECATION_WARNING='1',HISTCONTROL='',HISTIGNORE='')
  os.execl('/bin/bash','bash','--noprofile','--rcfile',str(rc),'-i')
 def read():
  out=b'';end=time.monotonic()+2
  while time.monotonic()<end:
   if select.select([fd],[],[],.1)[0]:
    try: out+=os.read(fd,65536)
    except OSError: break
    if out.endswith(b'PROBE> '): break
  return out
 def run(cmd):
  os.write(fd,(cmd+'\n').encode());out=read()
  starts=re.findall(rb'6973;C;probe-\d+;([^\x07]*)\x07',out)
  ends=re.findall(rb'6973;D;probe-\d+;(\d+)\x07',out)
  return out,[base64.b64decode(x).decode() for x in starts],ends
 try:
  read()
  for cmd in ['echo SIMPLE','echo ONE; (echo TWO)','printf PIPE | cat','false']:
   out,commands,ends=run(cmd)
   assert commands==[cmd],(cmd,commands,out)
   assert ends==([b'1'] if cmd=='false' else [b'0']),(cmd,ends,out)
  redirected=pathlib.Path(work)/'redirected'
  out,commands,ends=run(f"printf CLEAN > '{redirected}'")
  assert redirected.read_bytes()==b'CLEAN'
  run('HISTCONTROL=ignorespace')
  assert run(' echo OMITTED')[1]==[]
  run('set +o history')
  assert run('echo NOHISTORY')[1]==[]
  run('set -o history')
  assert run('echo BACK')[1]==['echo BACK']
  # An untraced subshell produces no command marker: never reuse the last label.
  assert run('(echo SUBSHELL)')[1]==[]
  # A background job is not complete just because the prompt returned.
  assert run('sleep 1 &')[2]==[]
  run('wait')
  # Replacing a DEBUG trap is the person's choice; never reinstall ours.
  run("trap ':' DEBUG")
  assert run('echo CUSTOM_TRAP')[1]==[]
  assert b"trap -- ':' DEBUG" in run('trap -p DEBUG')[0]
  print('PASS: real Bash command fences, compound/pipeline, failure, redirect, omitted history, disabled history, subshell refusal')
 finally:
  os.write(fd,b'exit\n');read();os.close(fd);os.waitpid(pid,0)
