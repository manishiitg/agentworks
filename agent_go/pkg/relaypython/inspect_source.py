"""Read-only AST inspection. Never imports or executes authored Python."""
import ast
import base64
import json
import os


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


def inspect_automation_source(files):
    """A source outline, not a Python execution plan. Authored modules never run."""
    graph = {"native": True, "nodes": [], "edges": [], "errors": []}
    try:
        if not isinstance(files, dict) or 'main.py' not in files:
            raise ValueError('Save main.py before viewing this automation.')
        trees = {}
        for file, source in files.items():
            if not file.endswith('.py') or '/' in file or '\\' in file:
                raise ValueError('Choose flat Python files from this automation.')
            trees[file[:-3]] = ast.parse(source, filename=file)
        imports, constants, functions = {}, {}, {}
        for module, tree in trees.items():
            imports[module], constants[module] = {}, {}
            for item in tree.body:
                if isinstance(item, ast.ImportFrom):
                    for alias in item.names:
                        imports[module][alias.asname or alias.name] = (item.module, alias.name)
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

        def literal(module, value):
            seen = set()
            while isinstance(value, ast.Name) and value.id in constants[module] and value.id not in seen:
                seen.add(value.id)
                value = constants[module][value.id]
            return value.value if isinstance(value, ast.Constant) and isinstance(value.value, str) else 'built at run time'

        def argument(call, index, name):
            return call.args[index] if len(call.args) > index else next((item.value for item in call.keywords if item.arg == name), None)

        def add(call, module, kind, label, identity, description=None):
            import hashlib
            node = dict(id='step-' + hashlib.sha256(identity.encode()).hexdigest()[:20], type=kind,
                        label=label, call=identity, line=call.lineno)
            if description is not None:
                node['description'] = description
            if graph['nodes']:
                graph['edges'].append({'from': graph['nodes'][-1]['id'], 'to': node['id'], 'line': call.lineno})
            graph['nodes'].append(node)

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
                    f'agent@{module}.py:{expression.lineno}', literal(module, argument(expression, 1, 'message')))
                return True
            fn, label = decorated(name_module, name) if name_module in trees else (None, None)
            if fn is not None:
                add(expression, module, 'script', label,
                    f'{name_module}.py:{name}@{module}.py:{expression.lineno}')
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
                    add(statement, module, 'script', 'Other code', f'other@{module}.py:{statement.lineno}')
                elif not walk_call(module, statement, expand) and any(isinstance(item, ast.Call) for item in ast.walk(statement)):
                    add(statement, module, 'script', 'Other code', f'other@{module}.py:{statement.lineno}')

        entry = functions.get(('main', 'main'))
        if entry:
            block('main', entry.body)
        else:
            block('main', trees['main'].body)
    except (SyntaxError, ValueError, RecursionError, TypeError) as error:
        graph['nodes'], graph['edges'] = [], []
        graph['errors'] = [str(error)]
    return graph


if __name__ == "__main__":
    if "VAR_AUTOMATION_CHECK_FILES" in os.environ:
        print(json.dumps(inspect_automation_source(json.loads(base64.b64decode(os.environ["VAR_AUTOMATION_CHECK_FILES"])))))
    else:
        print(json.dumps(inspect_source(base64.b64decode(os.environ["VAR_RELAY_CHECK_SOURCE"]).decode())))
