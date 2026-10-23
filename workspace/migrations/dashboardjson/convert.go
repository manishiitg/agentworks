// Package dashboardjson is the one-off JSON-dashboard to TSX migration.
// It is deliberately separate from dashboard authoring and rendering.
package dashboardjson

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type element struct {
	Type     string              `json:"type"`
	Props    map[string]any      `json:"props"`
	Children []string            `json:"children"`
	Slots    map[string][]string `json:"slots"`
	On       map[string]any      `json:"on"`
	Watch    map[string]any      `json:"watch"`
	Visible  any                 `json:"visible"`
	Repeat   map[string]any      `json:"repeat"`
}
type specification struct {
	Root     string             `json:"root"`
	Elements map[string]element `json:"elements"`
	State    map[string]any     `json:"state"`
	Data     []map[string]any   `json:"data"`
	Styles   string             `json:"styles"`
	Motion   map[string]any     `json:"motion"`
}
type scope struct{ item, index, path string }

func literal(v any) string { data, _ := json.Marshal(v); return string(data) }
func keys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}
func field(item, path string) string {
	if path == "" {
		return item
	}
	return "read(" + item + "," + literal(path) + ")"
}

var template = regexp.MustCompile(`\$\{([^}]+)\}`)

func expression(value any, at scope, action bool) string {
	if text, ok := value.(string); action && ok && text == "$id" {
		return "crypto.randomUUID()"
	}
	switch v := value.(type) {
	case []any:
		out := []string{}
		for _, x := range v {
			out = append(out, expression(x, at, action))
		}
		return "[" + strings.Join(out, ",") + "]"
	case map[string]any:
		if _, ok := v["$id"]; action && ok {
			return "crypto.randomUUID()"
		}
		if p, ok := v["$state"]; ok {
			return "store.get(" + literal(p) + ")"
		}
		if p, ok := v["$bindState"]; ok {
			return "store.get(" + literal(p) + ")"
		}
		if p, ok := v["$item"].(string); ok {
			if action {
				return at.path + "+" + literal("/"+p)
			}
			return field(at.item, p)
		}
		if p, ok := v["$bindItem"].(string); ok {
			return "store.get(" + at.path + "+" + literal("/"+p) + ")"
		}
		if _, ok := v["$index"]; ok {
			return at.index
		}
		if cond, ok := v["$cond"]; ok {
			return "(" + condition(cond, at) + "?" + expression(v["$then"], at, action) + ":" + expression(v["$else"], at, action) + ")"
		}
		if text, ok := v["$template"].(string); ok {
			out := []string{}
			last := 0
			for _, match := range template.FindAllStringSubmatchIndex(text, -1) {
				out = append(out, literal(text[last:match[0]]))
				p := text[match[2]:match[3]]
				x := "store.get(" + literal(p) + ")"
				if !strings.HasPrefix(p, "/") {
					x = "(" + field(at.item, p) + "??store.get(" + literal("/"+p) + "))"
				}
				out = append(out, "String("+x+"??'')")
				last = match[1]
			}
			out = append(out, literal(text[last:]))
			return "(" + strings.Join(out, "+") + ")"
		}
		out := []string{}
		for _, k := range keys(v) {
			out = append(out, literal(k)+":"+expression(v[k], at, action))
		}
		return "({" + strings.Join(out, ",") + "})"
	default:
		return literal(value)
	}
}
func condition(v any, at scope) string {
	if v == nil {
		return "true"
	}
	switch c := v.(type) {
	case bool:
		return literal(c)
	case []any:
		out := []string{}
		for _, x := range c {
			out = append(out, condition(x, at))
		}
		return "(" + strings.Join(out, "&&") + ")"
	case map[string]any:
		for _, op := range []string{"$and", "$or"} {
			if list, ok := c[op].([]any); ok {
				out := []string{}
				for _, x := range list {
					out = append(out, condition(x, at))
				}
				join := "&&"
				if op == "$or" {
					join = "||"
				}
				if len(out) == 0 {
					return literal(op == "$and")
				}
				return "(" + strings.Join(out, join) + ")"
			}
		}
		value := "undefined"
		if p, ok := c["$state"]; ok {
			value = "store.get(" + literal(p) + ")"
		}
		if p, ok := c["$item"].(string); ok {
			value = field(at.item, p)
		}
		if _, ok := c["$index"]; ok {
			value = at.index
		}
		out := "Boolean(" + value + ")"
		for _, op := range []string{"eq", "neq", "gt", "gte", "lt", "lte"} {
			if rhs, ok := c[op]; ok {
				symbol := map[string]string{"eq": "===", "neq": "!==", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[op]
				right := expression(rhs, at, false)
				out = "(" + value + symbol + right + ")"
				if op != "eq" && op != "neq" {
					out = "(typeof " + value + "==='number'&&typeof " + right + "==='number'&&" + out + ")"
				}
				break
			}
		}
		if c["not"] == true {
			return "!(" + out + ")"
		}
		return out
	}
	return "true"
}
func actionCode(raw any, at scope) (string, error) {
	if list, ok := raw.([]any); ok {
		out := ""
		for _, x := range list {
			s, err := actionCode(x, at)
			if err != nil {
				return "", err
			}
			out += s
		}
		return out, nil
	}
	a, ok := raw.(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid saved action")
	}
	p, _ := a["params"].(map[string]any)
	path := expression(p["statePath"], at, true)
	value := expression(p["value"], at, false)
	var code string
	switch a["action"] {
	case "setState":
		code = "store.set(" + path + "," + value + ");"
	case "pushState":
		code = "store.set(" + path + ",[...(store.get(" + path + ")??[])," + value + "]);"
	case "removeState":
		code = "store.set(" + path + ",(store.get(" + path + ")??[]).filter((_,i)=>i!==" + expression(p["index"], at, false) + "));"
	case "validateForm":
		code = "store.validateAll();"
	case "refreshData":
		code = "await refresh();"
	default:
		return "", fmt.Errorf("unsupported saved action %v", a["action"])
	}
	if cond, ok := a["condition"]; ok {
		code = "if(" + condition(cond, at) + "){" + code + "}"
	}
	return code, nil
}

type compiler struct {
	spec   specification
	active map[string]bool
	number int
}

func (c *compiler) node(id string, at scope) (string, error) {
	if c.active[id] {
		return "", fmt.Errorf("cyclic dashboard element %q", id)
	}
	e, ok := c.spec.Elements[id]
	if !ok {
		return "", fmt.Errorf("missing element %q", id)
	}
	c.active[id] = true
	defer delete(c.active, id)
	if !regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`).MatchString(e.Type) {
		return "", fmt.Errorf("invalid component %q", e.Type)
	}
	childScope := at
	repeatPath := ""
	if len(e.Repeat) > 0 {
		c.number++
		n := fmt.Sprint(c.number)
		repeatPath = expression(e.Repeat["statePath"], at, false)
		if p, ok := e.Repeat["statePath"].(string); ok {
			repeatPath = literal(p)
		}
		if p, ok := e.Repeat["statePath"].(map[string]any); ok {
			if item, ok := p["$item"].(string); ok {
				repeatPath = at.path + "+" + literal("/"+item)
			}
		}
		childScope = scope{"item" + n, "i" + n, "(" + repeatPath + "+'/'+i" + n + ")"}
	}
	children := []string{}
	for _, child := range e.Children {
		x, err := c.node(child, childScope)
		if err != nil {
			return "", err
		}
		children = append(children, x)
	}
	child := strings.Join(children, "")
	visible := condition(e.Visible, at)
	if repeatPath != "" {
		child = "{(store.get(" + repeatPath + ")??[]).map((" + childScope.item + "," + childScope.index + ")=><React.Fragment key={" + childScope.index + "}>{" + condition(e.Visible, childScope) + "&&<>" + child + "</>}</React.Fragment>)}"
		visible = "true"
	}
	props := []string{}
	bindings := []string{}
	for _, key := range keys(e.Props) {
		v := e.Props[key]
		props = append(props, literal(key)+":"+expression(v, at, false))
		if ref, ok := v.(map[string]any); ok {
			if path, ok := ref["$bindState"]; ok {
				bindings = append(bindings, literal(key)+":"+literal(path))
			}
			if path, ok := ref["$bindItem"].(string); ok {
				bindings = append(bindings, literal(key)+":"+at.path+"+"+literal("/"+path))
			}
		}
	}
	attrs := " {...{" + strings.Join(props, ",") + "}}"
	if len(bindings) > 0 {
		attrs += " bindings={{" + strings.Join(bindings, ",") + "}}"
	}
	events := []string{}
	for _, event := range keys(e.On) {
		code, err := actionCode(e.On[event], at)
		if err != nil {
			return "", err
		}
		events = append(events, literal(event)+":async()=>{"+code+"}")
	}
	if len(events) > 0 {
		attrs += " on={{" + strings.Join(events, ",") + "}}"
	}
	slots := []string{}
	for _, slot := range keys(e.Slots) {
		out := []string{}
		for _, n := range e.Slots[slot] {
			x, err := c.node(n, at)
			if err != nil {
				return "", err
			}
			out = append(out, x)
		}
		slots = append(slots, literal(slot)+":<>"+strings.Join(out, "")+"</>")
	}
	if len(slots) > 0 {
		attrs += " slots={{" + strings.Join(slots, ",") + "}}"
	}
	output := "<C." + e.Type + attrs + ">" + child + "</C." + e.Type + ">"
	for _, path := range keys(e.Watch) {
		code, err := actionCode(e.Watch[path], at)
		if err != nil {
			return "", err
		}
		output = "<Watch value={store.get(" + literal(path) + ")} run={async()=>{" + code + "}}>" + output + "</Watch>"
	}
	return "{" + visible + "&&(" + output + ")}", nil
}

// Convert emits normal React composition, not a JSON interpreter or a saved spec.
func Convert(source string) (string, error) {
	var spec specification
	if err := json.Unmarshal([]byte(source), &spec); err != nil {
		return "", err
	}
	if spec.Root == "" {
		return "", fmt.Errorf("dashboard root is missing")
	}
	c := compiler{spec: spec, active: map[string]bool{}}
	at := scope{"undefined", "undefined", "undefined"}
	jsx, err := c.node(spec.Root, at)
	if err != nil {
		return "", err
	}
	calls := []string{}
	for _, binding := range spec.Data {
		method, _ := binding["method"].(string)
		call := ""
		switch method {
		case "query":
			call = "report.query(" + literal(binding["sql"]) + "," + expression(binding["params"], at, false) + ")"
		case "get":
			call = "report.get(" + literal(binding["path"]) + ")"
		case "run":
			call = "report.run(" + literal(binding["path"]) + "," + expression(binding["args"], at, false) + ")"
		default:
			return "", fmt.Errorf("unknown saved data method %q", method)
		}
		calls = append(calls, call+".then(value=>["+literal(binding["statePath"])+",value])")
	}
	motion := ""
	clock := "undefined"
	if len(spec.Motion) > 0 {
		clock = "clock"
		motion = "if(!clock)clock=report.motion.create({id:'dashboard',duration:" + literal(spec.Motion["duration"]) + ",render:state=>store.set(" + literal(spec.Motion["statePath"]) + ",state)});"
	}
	return `import React, {useEffect,useRef,useState} from 'react';
import {components as C, createDashboardStore, DashboardProviders, useDashboardStore} from '@agentworks/dashboard';
function read(value,path){return String(path).replace(/^\//,'').split('/').reduce((v,k)=>v?.[k.replace(/~1/g,'/').replace(/~0/g,'~')],value)}
function Watch({value,run,children}){const previous=useRef(value);useEffect(()=>{if(previous.current!==value){previous.current=value;void run()}},[value,run]);return children}
const store=createDashboardStore(` + literal(spec.State) + `??{});
let clock;
export async function loadData(report){const values=await Promise.all([` + strings.Join(calls, ",") + `]);store.update(Object.fromEntries(values));` + motion + `return values}
function Content({report}){useDashboardStore();const refresh=()=>loadData(report);return <>` + jsx + `</>}
export default function Dashboard({report}){return <DashboardProviders store={store} refresh={()=>loadData(report)} clock={` + clock + `}><style>{` + literal(spec.Styles) + `}</style><Content report={report}/></DashboardProviders>}
`, nil
}
