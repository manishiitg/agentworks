#!/usr/bin/env python3
"""Delete Go functions (and their doc comments) by name.

Usage: scripts/drop-go-funcs.py path/to/file.go:FuncName [more file:Name ...]

Used with scripts/check-unused.py go: run the checker, feed its "file: name" lines here
(as file:name), repeat until it reports nothing, then fix unused imports and gofmt.
It understands receivers, generics and interface{} in signatures. It does not compile
anything: review the diff and let the deploy build be the check.
"""
import re,sys
def drop(s,name):
    m=re.search(r'\n((?://[^\n]*\n)*)func (?:\([^)]*\) )?'+re.escape(name)+r'\s*[\[(]',s)
    if not m: return s,False
    start=m.start()+1
    i=m.end()-1; depth=0
    while True:
        c=s[i]
        if c=='(': depth+=1
        elif c==')':
            depth-=1
            if depth==0: break
        i+=1
    j=i+1
    while True:
        c=s[j]
        if c=='{':
            before=s[max(0,j-12):j].rstrip()
            if before.endswith('interface') or before.endswith('struct') or s[j+1]=='}':
                j+=2 if s[j+1]=='}' else 1; continue
            break
        j+=1
    depth=0; instr=None
    while j<len(s):
        c=s[j]
        if instr:
            if instr=='`':
                if c=='`': instr=None
            elif c=='\\': j+=1
            elif c==instr: instr=None
        else:
            if c in '"`\'': instr=c
            elif c=='/' and s[j+1]=='/':
                j=s.index('\n',j); continue
            elif c=='{': depth+=1
            elif c=='}':
                depth-=1
                if depth==0: break
        j+=1
    end=j+1
    while end<len(s) and s[end]=='\n': end+=1
    return s[:start]+s[end:],True
for spec in sys.argv[1:]:
    f,n=spec.split(':')
    s=open(f,encoding='utf-8').read()
    s,ok=drop(s,n); print(n,ok)
    open(f,'w',encoding='utf-8').write(s)
