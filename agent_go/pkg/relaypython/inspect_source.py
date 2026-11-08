"""Read-only AST inspection. Never imports or executes authored Python."""
import ast
import base64
import contextlib
import json
import os
import types


def inspect_source(source):
    graph = {"native": False, "nodes": [], "edges": [], "errors": []}
    try:
        tree = ast.parse(source, filename="relay.py")
        compile(tree, "relay.py", "exec")
        roots = [node for node in tree.body if isinstance(node, ast.AsyncFunctionDef) and node.name == "run"]
        if len(roots) != 1:
            raise ValueError("Define one async def run(INPUT), or the existing run(INPUT, ctx)")
        root = roots[0]
        args = root.args
        names = [arg.arg for arg in args.posonlyargs + args.args]
        if args.vararg or args.kwarg or args.kwonlyargs or args.defaults:
            raise ValueError("run must accept exactly INPUT, or INPUT and ctx")
        if names not in (["INPUT"], ["INPUT", "ctx"]):
            raise ValueError("run must accept exactly INPUT, or INPUT and ctx")
        if names == ["INPUT", "ctx"]:
            return graph
        graph["native"] = True
        dbos_names, agent_names, classifier_names, platform_names = {"DBOS"}, {"agent"}, set(), set()
        constants = {}
        for node in tree.body:
            if isinstance(node, ast.ImportFrom):
                for name in node.names:
                    if node.module == "dbos" and name.name == "DBOS":
                        dbos_names.add(name.asname or name.name)
                    if node.module == "agentworks" and name.name == "agent":
                        agent_names.add(name.asname or name.name)
                    if node.module == "agentworks" and name.name == "classifier":
                        classifier_names.add(name.asname or name.name)
            if isinstance(node, ast.Import):
                for name in node.names:
                    if name.name == "agentworks":
                        platform_names.add(name.asname or name.name)
            if isinstance(node, ast.Assign) and len(node.targets) == 1 and isinstance(node.targets[0], ast.Name):
                constants[node.targets[0].id] = node.value

        def platform_call(item, names, member):
            if not isinstance(item, ast.Call):
                return False
            target = item.func
            return (isinstance(target, ast.Name) and target.id in names or
                    isinstance(target, ast.Attribute) and target.attr == member and
                    isinstance(target.value, ast.Name) and target.value.id in platform_names)

        def display_value(value):
            # Resolve only literal constants. Never evaluate authored expressions.
            original = value
            seen = set()
            while isinstance(value, ast.Name) and value.id in constants and value.id not in seen:
                seen.add(value.id)
                value = constants[value.id]
            try:
                return ast.literal_eval(value)
            except (ValueError, TypeError):
                return ast.unparse(original)

        def decorator(fn, kind):
            for item in fn.decorator_list:
                call = item if isinstance(item, ast.Call) else None
                target = call.func if call else item
                if isinstance(target, ast.Attribute) and target.attr == kind and isinstance(target.value, ast.Name) and target.value.id in dbos_names:
                    return call or item

        if decorator(root, "workflow") is None:
            raise ValueError("run(INPUT) requires @DBOS.workflow() and DBOS recovery enabled")
        functions = {node.name: node for node in tree.body
                     if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
                     and (decorator(node, "step") is not None or decorator(node, "workflow") is not None)}

        def readable(value):
            return value.replace("_", " ").replace("-", " ").capitalize()

        def add_node(id, kind, label, line, **fields):
            if not any(node["id"] == id for node in graph["nodes"]):
                graph["nodes"].append(dict(id=id, type=kind, label=label, line=line, **fields))
            return id

        def connect(tails, target):
            for previous, label in tails:
                edge = dict(from_=previous, to=target, line=0)
                edge["from"] = edge.pop("from_")
                if label:
                    edge["label"] = label
                if edge not in graph["edges"]:
                    graph["edges"].append(edge)

        start = add_node("workflow-input", "input", "Receive input", root.lineno)
        result = "workflow-result"

        def calls_in(node):
            found = []
            class Calls(ast.NodeVisitor):
                def visit_Call(self, call):
                    self.generic_visit(call)
                    if isinstance(call.func, ast.Name) and call.func.id in functions and call.func.id != "run":
                        found.append(call)
                def visit_FunctionDef(self, node): pass
                def visit_AsyncFunctionDef(self, node): pass
                def visit_Lambda(self, node): pass
            Calls().visit(node)
            return found

        def calls(node, tails):
            for call in calls_in(node):
                fn = functions[call.func.id]
                decoration = decorator(fn, "step") or decorator(fn, "workflow")
                name = fn.name
                if isinstance(decoration, ast.Call):
                    for keyword in decoration.keywords:
                        if keyword.arg == "name" and isinstance(keyword.value, ast.Constant) and isinstance(keyword.value.value, str):
                            name = keyword.value.value
                classifiers = [item for item in ast.walk(fn) if platform_call(item, classifier_names, "classifier")]
                kind = "classifier" if classifiers else "agent" if any(platform_call(item, agent_names, "agent") for item in ast.walk(fn)) else "script"
                fields = {}
                if classifiers:
                    fields = {keyword.arg: display_value(keyword.value) for keyword in classifiers[0].keywords
                              if keyword.arg in ("model", "categories", "instructions")}
                    fields.setdefault("model", "clef-flash")
                    fields["provider"] = "cloudflare"
                id = add_node(fn.name[:48] + "-" + str(call.lineno), kind, readable(name), call.lineno,
                              call=name, description=ast.get_docstring(fn) or "", input=ast.unparse(call)[:500], **fields)
                connect(tails, id)
                tails = [(id, "")]
            return tails

        def block(statements, tails):
            for stmt in statements:
                if not tails:
                    break
                if isinstance(stmt, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                    continue
                if isinstance(stmt, ast.If):
                    tails = calls(stmt.test, tails)
                    decision = add_node("decision-" + str(stmt.lineno), "decision", ast.unparse(stmt.test)[:100], stmt.lineno)
                    connect(tails, decision)
                    tails = block(stmt.body, [(decision, "Yes")]) + block(stmt.orelse, [(decision, "No")])
                elif isinstance(stmt, (ast.For, ast.AsyncFor, ast.While)):
                    expression = stmt.test if isinstance(stmt, ast.While) else stmt.iter
                    tails = calls(expression, tails)
                    loop = add_node("loop-" + str(stmt.lineno), "decision", "Repeat: " + ast.unparse(expression)[:90], stmt.lineno)
                    connect(tails, loop)
                    connect([(id, "Next") for id, _ in block(stmt.body, [(loop, "Each")])], loop)
                    tails = block(stmt.orelse, [(loop, "Done")])
                elif isinstance(stmt, ast.Return):
                    tails = calls(stmt, tails)
                    add_node(result, "output", "Return result", stmt.lineno)
                    connect(tails, result)
                    tails = []
                elif isinstance(stmt, (ast.With, ast.AsyncWith)):
                    for item in stmt.items:
                        tails = calls(item.context_expr, tails)
                    tails = block(stmt.body, tails)
                elif isinstance(stmt, ast.Try):
                    # Exception routes depend on runtime failures; mark them as
                    # alternatives rather than pretending to execute Python.
                    incoming = tails
                    tails = block(stmt.orelse, block(stmt.body, incoming))
                    for handler in stmt.handlers:
                        tails += block(handler.body, [(id, "On error") for id, _ in incoming])
                    tails = block(stmt.finalbody, tails)
                else:
                    tails = calls(stmt, tails)
            return tails

        tails = block(root.body, [(start, "")])
        if tails or not any(node["id"] == result for node in graph["nodes"]):
            add_node(result, "output", "Return result", root.end_lineno)
            connect(tails, result)
        if len(graph["nodes"]) > 200 or len(graph["edges"]) > 400:
            raise ValueError("Source overview exceeds 200 nodes or 400 edges")
    except (SyntaxError, ValueError, RecursionError) as error:
        graph["errors"] = [str(error)]
        graph["validation_error"] = str(error)
    return graph


def python_type_notes(files):
    """Optional static hints. Never import authored files or inherit credentials."""
    import pathlib
    import shutil
    import subprocess
    import sys
    import tempfile
    checker = shutil.which('pyright') or shutil.which('basedpyright')
    if not checker:
        return [], ['The checker is not installed here. The script can still run.']
    root = pathlib.Path.cwd().resolve()
    saved = [file.resolve() for name in files for file in [root / name]
             if not file.is_symlink() and file.is_file() and file.resolve().is_relative_to(root)]
    sdk = next((entry for entry in os.environ.get('PYTHONPATH', '').split(os.pathsep)
                if entry and (pathlib.Path(entry) / 'agentworks' / '__init__.py').is_file()), None)
    if not saved or not sdk:
        return [], ['Type hints could not be checked this time. The script can still run.']
    try:
        with tempfile.TemporaryDirectory(prefix='python-hints-') as directory:
            config = pathlib.Path(directory) / 'pyrightconfig.json'
            config.write_text(json.dumps({'include': [os.path.relpath(file, directory) for file in saved],
                'extraPaths': [sdk, str(root)], 'typeCheckingMode': 'basic',
                'useLibraryCodeForTypes': True}), encoding='utf-8')
            environment = {'PATH': os.environ.get('PATH', ''), 'HOME': directory,
                           'TMPDIR': directory, 'LANG': 'C.UTF-8'}
            result = subprocess.run([checker, '--outputjson', '--project', str(config),
                                     '--pythonpath', sys.executable], cwd=root,
                                    env=environment, capture_output=True, text=True, timeout=10)
            if result.returncode not in (0, 1):
                raise ValueError('checker unavailable')
            diagnostics = json.loads(result.stdout)['generalDiagnostics']
            notes = []
            for item in diagnostics:
                file = pathlib.Path(item['file']).resolve()
                if file not in saved:
                    continue
                line = item.get('range', {}).get('start', {}).get('line', 0)
                notes.append({'file': file.relative_to(root).as_posix(),
                              'line': max(1, int(line) + 1), 'message': str(item['message'])})
            summary = []
            if notes:
                noun = 'problem' if len(notes) == 1 else 'problems'
                summary = [f'Type hints show {len(notes)} possible {noun}. The script can still run.']
            return notes, summary
    except (OSError, ValueError, KeyError, TypeError, subprocess.TimeoutExpired):
        return [], ['Type hints could not be checked this time. The script can still run.']


def inspect_automation_source(files):
    """A source outline, not a Python execution plan. Authored modules never run."""
    graph = {"native": True, "nodes": [], "edges": [], "errors": [], "unkeyed_calls": [], "notes": []}
    try:
        if not isinstance(files, dict) or 'main.py' not in files:
            raise ValueError('Save main.py before viewing this automation.')
        trees, filenames, sources = {}, {}, {}
        for file, source in files.items():
            parts = file.split('/')
            if (not file.endswith('.py') or '\\' in file or
                    any(not part or part.startswith('.') or part == '__pycache__' for part in parts)):
                raise ValueError('Choose relative Python files from this automation.')
            module = file[:-3].replace('/', '.')
            if parts[-1] == '__init__.py':
                module = '.'.join(parts[:-1]) or '__init__'
            if module in trees:
                raise ValueError('Python module has two source files: ' + module)
            trees[module], filenames[module], sources[module] = ast.parse(source, filename=file), file, source.splitlines()
        imports, constants, functions = {}, {}, {}
        for module, tree in trees.items():
            imports[module], constants[module] = {}, {}
            for item in tree.body:
                if isinstance(item, ast.ImportFrom):
                    for alias in item.names:
                        origin = item.module
                        if item.level:
                            package = module if filenames[module].endswith('/__init__.py') else module.rpartition('.')[0]
                            parents = package.split('.') if package else []
                            prefix = parents[:len(parents) - item.level + 1]
                            origin = '.'.join(prefix + ([item.module] if item.module else []))
                        imports[module][alias.asname or alias.name] = (origin, alias.name)
                elif isinstance(item, ast.Import):
                    for alias in item.names:
                        imports[module][alias.asname or alias.name] = (alias.name, None)
                elif isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    functions[module, item.name] = item
                elif isinstance(item, ast.Assign) and len(item.targets) == 1 and isinstance(item.targets[0], ast.Name):
                    constants[module][item.targets[0].id] = item.value
                elif isinstance(item, ast.AnnAssign) and isinstance(item.target, ast.Name) and item.value:
                    constants[module][item.target.id] = item.value

        def target(module, expression):
            if isinstance(expression, ast.Name):
                return imports[module].get(expression.id, (module, expression.id))
            if isinstance(expression, ast.Attribute) and isinstance(expression.value, ast.Name):
                imported = imports[module].get(expression.value.id)
                if imported and imported[1] is None:
                    return imported[0], expression.attr
            return None, None

        # Inventory only calls whose imported target is known. No source is
        # rewritten: a loop/helper needs the author's logical work identity.
        for module, tree in trees.items():
            for call in ast.walk(tree):
                if not isinstance(call, ast.Call):
                    continue
                origin, helper = target(module, call.func)
                if origin != 'agentworks' or helper not in ('agent', 'run_script'):
                    continue
                key = next((item.value for item in call.keywords if item.arg == 'key'), None)
                if key is None or isinstance(key, ast.Constant) and key.value is None:
                    graph['unkeyed_calls'].append({'file': filenames[module], 'line': call.lineno, 'helper': helper})
        graph['unkeyed_calls'].sort(key=lambda item: (item['file'], item['line'], item['helper']))
        if graph['unkeyed_calls']:
            graph['notes'].append('Some agent or saved-script calls have no visible saved-result key. Add a stable key to costly work you want to reuse after a failure, such as one key per record. Scripts still run without keys.')

        def literal(module, value):
            seen = set()
            while True:
                if isinstance(value, ast.Constant) and isinstance(value.value, str):
                    return value.value
                if isinstance(value, ast.Name) and value.id in constants[module]:
                    origin = module, value.id
                else:
                    origin = target(module, value)
                if origin in seen or origin[0] not in constants or origin[1] not in constants[origin[0]]:
                    return 'built at run time'
                seen.add(origin)
                module = origin[0]
                value = constants[module][origin[1]]

        def argument(call, index, name):
            return call.args[index] if len(call.args) > index else next((item.value for item in call.keywords if item.arg == name), None)

        parents, last = [], {}

        def add(call, module, kind, label, identity, description=None, role=None):
            import hashlib
            parent = parents[-1] if parents else None
            # One row per occurrence: the same step reached in two cases gets two ids.
            node = dict(id='step-' + hashlib.sha256((identity + '|' + (parent or '')).encode()).hexdigest()[:20], type=kind,
                        label=label, call=identity, line=call.lineno)
            if description is not None:
                node['description'] = description
            if role:
                node['role'] = role
            if parent:
                node['parent'] = parent
            if any(existing['id'] == node['id'] for existing in graph['nodes']):
                return node['id']
            if len(graph['nodes']) >= 200:
                raise ValueError('Source overview exceeds 200 items')
            # Edges join siblings in source order; nesting is carried by `parent`.
            if last.get(parent):
                graph['edges'].append({'from': last[parent], 'to': node['id'], 'line': call.lineno})
            last[parent] = node['id']
            graph['nodes'].append(node)
            return node['id']

        @contextlib.contextmanager
        def nested(node_id):
            parents.append(node_id)
            last.pop(node_id, None)
            try:
                yield
            finally:
                parents.pop()

        def line_text(module, number):
            lines = sources[module]
            return lines[number - 1] if 0 < number <= len(lines) else ''

        def comment_above(module, number):
            text = line_text(module, number - 1).strip()
            return text.lstrip('#').strip() or None if text.startswith('#') else None

        def plain(value, limit=80):
            return value if len(value) <= limit else value[:limit - 3] + '...'

        def operand(value):
            if isinstance(value, ast.Constant):
                return value.value if isinstance(value.value, str) else str(value.value)
            if isinstance(value, (ast.Tuple, ast.List, ast.Set)) and value.elts:
                return ' or '.join(operand(item) for item in value.elts)
            if isinstance(value, (ast.Name, ast.Attribute)):
                return ast.unparse(value).replace('_', ' ')
            return ast.unparse(value)

        def words(value):
            """A short plain-words reading of a condition. Never evaluates it."""
            compare = {ast.Eq: 'is', ast.NotEq: 'is not', ast.In: 'is one of', ast.NotIn: 'is not one of',
                       ast.Lt: 'is less than', ast.Gt: 'is more than', ast.LtE: 'is at most',
                       ast.GtE: 'is at least', ast.Is: 'is', ast.IsNot: 'is not'}
            if isinstance(value, ast.Compare) and len(value.ops) == 1 and type(value.ops[0]) in compare:
                text = f'{operand(value.left)} {compare[type(value.ops[0])]} {operand(value.comparators[0])}'
            elif isinstance(value, ast.BoolOp):
                text = (' and ' if isinstance(value.op, ast.And) else ' or ').join(words(item) for item in value.values)
            elif isinstance(value, ast.UnaryOp) and isinstance(value.op, ast.Not):
                text = 'not ' + words(value.operand)
            elif isinstance(value, (ast.Name, ast.Attribute)):
                text = operand(value)
            else:
                text = ast.unparse(value)
            return plain(text)

        def has_step(module, node):
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.Lambda)):
                return False
            if isinstance(node, ast.Call):
                name_module, name = target(module, node.func)
                if (name_module, name) == ('agentworks', 'agent'):
                    return True
                if name_module in trees and decorated(name_module, name)[0] is not None:
                    return True
            return any(has_step(module, child) for child in ast.iter_child_nodes(node))

        def container(module, number, role, label, identity):
            return add(types.SimpleNamespace(lineno=number), module, 'decision', label,
                       f'{identity}@{filenames[module]}:{number}', role=role)

        def decorated(module, name):
            fn = functions.get((module, name))
            if fn:
                for decoration in fn.decorator_list:
                    if isinstance(decoration, ast.Call) and target(module, decoration.func) == ('agentworks', 'step'):
                        return fn, literal(module, argument(decoration, 0, 'title'))
            return None, None

        def walk_call(module, expression, expand=True):
            found = False
            # Visit arguments before their surrounding call, as Python does.
            for child in ast.iter_child_nodes(expression):
                if isinstance(child, (ast.Lambda, ast.FunctionDef, ast.AsyncFunctionDef)):
                    continue
                if walk_call(module, child, expand):
                    found = True
            if not isinstance(expression, ast.Call):
                return found
            name_module, name = target(module, expression.func)
            if (name_module, name) == ('agentworks', 'agent'):
                add(expression, module, 'agent', literal(module, argument(expression, 0, 'name')),
                    f'agent@{filenames[module]}:{expression.lineno}', literal(module, argument(expression, 1, 'message')))
                return True
            fn, label = decorated(name_module, name) if name_module in trees else (None, None)
            if fn is not None:
                add(expression, module, 'script', label,
                    f'{filenames[name_module]}:{name}@{filenames[module]}:{expression.lineno}')
                if expand:
                    block(name_module, fn.body, False)
                return True
            return found

        def block(module, statements, expand=True):
            for statement in statements:
                if len(graph['nodes']) >= 200:
                    raise ValueError('Source overview exceeds 200 items')
                if isinstance(statement, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.Import, ast.ImportFrom, ast.Pass)):
                    continue
                if isinstance(statement, (ast.If, ast.For, ast.AsyncFor, ast.While, ast.Try, ast.With, ast.AsyncWith, ast.Match)):
                    if has_step(module, statement):
                        structured(module, statement, expand)
                    else:
                        add(statement, module, 'script', 'Other code', f'other@{filenames[module]}:{statement.lineno}')
                elif not walk_call(module, statement, expand) and any(isinstance(item, ast.Call) for item in ast.walk(statement)):
                    add(statement, module, 'script', 'Other code', f'other@{filenames[module]}:{statement.lineno}')

        def structured(module, statement, expand):
            """Draw a block that holds steps: a container row, with its steps nested inside."""
            if isinstance(statement, ast.If):
                walk_call(module, statement.test, expand)
                branch = container(module, statement.lineno, 'branch', 'Choose a path', 'branch')
                with nested(branch):
                    current, first = statement, True
                    while True:
                        case = container(module, current.lineno, 'case',
                                         comment_above(module, current.lineno) or words(current.test), 'case')
                        with nested(case):
                            if not first:
                                walk_call(module, current.test, expand)
                            block(module, current.body, expand)
                        rest = current.orelse
                        if len(rest) == 1 and isinstance(rest[0], ast.If) and line_text(module, rest[0].lineno).lstrip().startswith('elif'):
                            current, first = rest[0], False
                            continue
                        if rest:
                            number = next((n for n in range(rest[0].lineno - 1, current.body[-1].end_lineno, -1)
                                           if line_text(module, n).strip().startswith('else')), rest[0].lineno)
                            case = container(module, number, 'case', comment_above(module, number) or 'Otherwise', 'case')
                            with nested(case):
                                block(module, rest, expand)
                        break
            elif isinstance(statement, (ast.For, ast.AsyncFor, ast.While)):
                if isinstance(statement, ast.While):
                    default = 'Repeat while ' + words(statement.test)
                else:
                    walk_call(module, statement.iter, expand)
                    default = 'Repeat for each ' + operand(statement.target)
                repeat = container(module, statement.lineno, 'repeat', comment_above(module, statement.lineno) or default, 'repeat')
                with nested(repeat):
                    if isinstance(statement, ast.While):
                        walk_call(module, statement.test, expand)
                    block(module, statement.body + statement.orelse, expand)
            elif isinstance(statement, ast.Try):
                outer = container(module, statement.lineno, 'try', comment_above(module, statement.lineno) or 'Try', 'try')
                with nested(outer):
                    block(module, statement.body, expand)
                    for handler in statement.handlers:
                        failure = container(module, handler.lineno, 'failure', comment_above(module, handler.lineno) or 'If this fails', 'failure')
                        with nested(failure):
                            block(module, handler.body, expand)
                    block(module, statement.orelse, expand)
                    if statement.finalbody:
                        number = next((n for n in range(statement.finalbody[0].lineno - 1, statement.finalbody[0].lineno - 4, -1)
                                       if line_text(module, n).strip().startswith('finally')), statement.finalbody[0].lineno)
                        always = container(module, number, 'block', comment_above(module, number) or 'Always', 'always')
                        with nested(always):
                            block(module, statement.finalbody, expand)
            elif isinstance(statement, (ast.With, ast.AsyncWith)):
                outer = container(module, statement.lineno, 'block', comment_above(module, statement.lineno) or 'Block', 'block')
                with nested(outer):
                    for item in statement.items:
                        walk_call(module, item.context_expr, expand)
                    block(module, statement.body, expand)
            else:
                walk_call(module, statement.subject, expand)
                branch = container(module, statement.lineno, 'branch', 'Choose a path', 'branch')
                with nested(branch):
                    for item in statement.cases:
                        number = item.pattern.lineno
                        if item.guard is None and isinstance(item.pattern, ast.MatchAs) and item.pattern.pattern is None:
                            default = 'Otherwise'
                        elif isinstance(item.pattern, ast.MatchValue):
                            default = f'{operand(statement.subject)} is {operand(item.pattern.value)}'
                        else:
                            default = plain(ast.unparse(item.pattern))
                        if item.guard is not None and default != 'Otherwise':
                            default = plain(f'{default}, and {words(item.guard)}')
                        case = container(module, number, 'case', comment_above(module, number) or default, 'case')
                        with nested(case):
                            if item.guard is not None:
                                walk_call(module, item.guard, expand)
                            block(module, item.body, expand)

        entry = functions.get(('main', 'main'))
        if entry:
            block('main', entry.body)
        else:
            block('main', trees['main'].body)
    except (SyntaxError, ValueError, RecursionError, TypeError) as error:
        graph['nodes'], graph['edges'] = [], []
        graph['errors'] = [str(error)]
    if os.environ.get('VAR_AUTOMATION_TYPE_CHECK') == '1' and isinstance(files, dict):
        graph['type_notes'], notes = python_type_notes(files)
        graph['notes'].extend(notes)
    return graph


if __name__ == "__main__":
    if "VAR_AUTOMATION_CHECK_FILES" in os.environ:
        print(json.dumps(inspect_automation_source(json.loads(base64.b64decode(os.environ["VAR_AUTOMATION_CHECK_FILES"])))))
    else:
        print(json.dumps(inspect_source(base64.b64decode(os.environ["VAR_RELAY_CHECK_SOURCE"]).decode())))
