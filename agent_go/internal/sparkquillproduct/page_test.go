package sparkquillproduct

import (
	"io/fs"
	"strings"
	"testing"
)

func TestReadingBookTemplateSurvivesActivityRendering(t *testing.T) {
	source, err := fs.ReadFile(SkillFiles, "skills/reading-book/assets/book.sq.html")
	if err != nil {
		t.Fatal(err)
	}
	page, report, err := RenderActivityPage(string(source), PageMeta{Title: "A seed's journey"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Dropped) != 0 {
		t.Fatalf("reading book lost content or controls: %v", report.Dropped)
	}
	if count := strings.Count(page, `<article class="book-page">`); count != 6 {
		t.Fatalf("book has %d pages after rendering, want 6", count)
	}
	for _, needed := range []string{`id="book-prev"`, `id="book-next"`, `aria-live="polite"`, `@media print`, `SQ.saveGame('reading-progress'`, `SQ.loadGame('reading-progress'`, `window.SQ={`} {
		if !strings.Contains(page, needed) {
			t.Fatalf("finished book lost reading functionality %q", needed)
		}
	}
}

func TestVideoLessonKeepsLocalPlaybackAndCaptions(t *testing.T) {
	page, report, err := RenderActivityPage(`<section data-role="learn"><h1>Water cycle</h1><video controls playsinline preload="metadata" src="lesson.mp4"><track kind="captions" srclang="en" label="English" src="captions.vtt" default></video><p>The sun warms water.</p></section>`, PageMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Dropped) != 0 || len(report.Sections) != 1 {
		t.Fatalf("video lesson render report: %+v", report)
	}
	for _, needed := range []string{`<video controls="" playsinline="" preload="metadata" src="lesson.mp4">`, `src="captions.vtt"`, `The sun warms water.`} {
		if !strings.Contains(page, needed) {
			t.Fatalf("video lesson lost %q", needed)
		}
	}
}

// A page the way Quill might write it: its own look, a whole document, and
// the four conventions the tutor relies on.
const samplePage = `<!doctype html>
<html><head><title>Fractions, Maya's way</title>
<style>body{font-family:Georgia;background:#fff8e1}.spin{animation:spin 2s linear infinite}@keyframes spin{to{transform:rotate(1turn)}}</style>
<link rel="stylesheet" href="https://fonts.example/x.css">
</head><body>
<header class="hero"><h1>Adding fractions with different denominators</h1><p>Hi Maya!</p></header>
<section data-role="learn" class="lesson">
  <h2>Why the pieces must match</h2>
  <p>You can only add pieces that are the <strong>same size</strong>.</p>
  <div class="my-callout">The denominator tells you how big each piece is.</div>
  <figure><div class="jxgbox" style="width:300px;height:200px"></div><script>JXG.JSXGraph.initBoard('fig', {})</script><figcaption>Two thirds</figcaption></figure>
  <div class="spin">✦</div>
</section>
<section data-role="practice">
  <h2>Try it</h2>
  <div class="q"><p>1/2 + 1/3</p></div>
  <details><summary>Reveal</summary><p>5/6</p></details>
  <input type="text" placeholder="type here">
</section>
<section data-role="check">
  <h2>Quick check</h2>
  <div class="q card" data-marks="2"><p>3/5 + 1/4</p></div>
  <div class="q" data-marks="3"><p>Riya ate 2/5 of a cake and Aarav 1/3. How much is left?</p></div>
  <button data-choose="I need a hint">Hint please</button>
  <a href="https://example.com">a link</a>
  <img src="https://evil.example/x.png"><img src="pizza.png" alt="a pizza">
  <script src="https://cdn.example/lib.js"></script>
</section>
<section data-role="explore"><h2>Wonder</h2><div class="q"><p>Design your own fraction puzzle.</p></div></section>
<section data-role="bogus"><p>after</p></section>
</body></html>`

func TestRenderActivityPageAddsOnlyTheMachinery(t *testing.T) {
	page, report, err := RenderActivityPage(samplePage, PageMeta{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<title>Fractions, Maya&#39;s way</title>`,
		`<style>body{font-family:Georgia;background:#fff8e1}.spin{animation:spin 2s linear infinite}`, // Quill's look, untouched
		`<header class="hero"><h1>Adding fractions with different denominators</h1>`,
		`<section id="s1" data-role="learn" class="lesson">`,
		`<section id="s2" data-role="practice">`,
		`<section id="s3" data-role="check">`,
		`<section id="s4" data-role="explore">`,
		`<section id="s5" data-role="learn">`, // unknown role falls back to learn
		`<div class="my-callout">`,            // Quill's own blocks pass through
		`<figure id="fig1">`, `JXG.JSXGraph.initBoard`,
		`<div id="q1" class="q"><p>1/2 + 1/3</p><div class="answer-space"></div></div>`,
		`<div id="q2" class="q card" data-marks="2">`, `<div id="q3" class="q" data-marks="3">`, `<div id="q4" class="q">`,
		`<button data-choose="I need a hint">Hint please</button>`,
		`button[data-choose]`, `window.SQ={`, `answer:function(qid,value,el)`, `op==='print'`,
		`<input type="text" placeholder="type here">`,
		`<img src="pizza.png" alt="a pizza">`,
		`<p>5/6</p>`, // click-to-reveal is unwrapped, its content stays visible
		`a link`,     // links become plain text
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q\n%s", want, page)
		}
	}
	for _, bad := range []string{`<details`, `<summary`, `href="http`, `evil.example`, `cdn.example`, `fonts.example`} {
		if strings.Contains(page, bad) {
			t.Fatalf("page still carries %q", bad)
		}
	}
	if report.Title != "Fractions, Maya's way" || report.Questions != 4 || report.Marks != 5 || len(report.Sections) != 5 {
		t.Fatalf("report = %+v", report)
	}
	if report.Sections[2].Role != RoleCheck || strings.Join(report.Sections[2].Questions, ",") != "q2,q3" || report.Sections[1].Questions[0] != "q1" || report.Sections[3].Role != RoleExplore {
		t.Fatalf("section map = %+v", report.Sections)
	}
	joined := strings.Join(report.Dropped, " | ")
	for _, want := range []string{"<details>", `<img src="https://evil.example/x.png"> (remote`, `<script src="https://cdn.example/lib.js">`, "<link> in <head>"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("dropped = %v, want %q", report.Dropped, want)
		}
	}
	if !strings.Contains(strings.Join(report.Warnings, " | "), `unknown section role "bogus"`) {
		t.Fatalf("warnings = %v", report.Warnings)
	}
}

func TestRenderActivityPageAcceptsAFragmentAndRejectsEmpty(t *testing.T) {
	page, _, err := RenderActivityPage(`<h1>Notes</h1><p>Just a paragraph.</p><div class="q"><p>2+2</p></div>`, PageMeta{})
	if err != nil || !strings.Contains(page, `<title>Notes</title>`) || !strings.Contains(page, `<div id="q1" class="q">`) {
		t.Fatalf("fragment: err=%v\n%s", err, page)
	}
	if _, _, err := RenderActivityPage(`   `, PageMeta{}); err == nil {
		t.Fatal("an empty page must be an error")
	}
}

func TestRenderActivityPageKeepsAnswerWidgets(t *testing.T) {
	source := `<section data-role="check"><div class="q"><p>2 + 2?</p><form><input id="answer" type="text"><button type="button" onclick="SQ.answer('q1',document.getElementById('answer').value,this)">Done</button></form><script>SQ.startTimers([{qid:'q1',seconds:60}])</script></div></section>`
	page, _, err := RenderActivityPage(source, PageMeta{Title: "Quick test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<input id="answer" type="text">`, `onclick="SQ.answer(&#39;q1&#39;,document.getElementById(&#39;answer&#39;).value,this)"`, `answer:function(qid,value,el)`, `op:'answer'`} {
		if !strings.Contains(page, want) {
			t.Fatalf("answer widget lacks %q\n%s", want, page)
		}
	}
	if strings.Contains(page, `<form`) {
		t.Fatal("form wrapper should be removed without discarding its answer widget")
	}
	if strings.Index(page, `window.SQ={`) > strings.Index(page, `SQ.startTimers([{qid:'q1',seconds:60}])`) {
		t.Fatal("the SQ bridge must load before the test's own timer script")
	}
}

func TestRenderActivityPageKeepsPlayableGameAndProgressBridge(t *testing.T) {
	source := `<section><h1>Fraction Quest</h1><button id="advance">Next room</button><script>document.getElementById('advance').onclick=function(){SQ.saveGame('progress',{room:2})};SQ.loadGame('progress',function(saved){window.room=saved?saved.room:1})</script></section>`
	page, _, err := RenderActivityPage(source, PageMeta{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`saveGame:function(key,data)`, `loadGame:function(key,done)`, `op:'game-save'`, `op:'game-load'`, `id="advance"`, `SQ.saveGame('progress',{room:2})`} {
		if !strings.Contains(page, want) {
			t.Fatalf("playable game lost %q", want)
		}
	}
	if strings.Index(page, `window.SQ={`) > strings.Index(page, `SQ.loadGame('progress'`) {
		t.Fatal("game bridge must load before the game's own script")
	}
}
