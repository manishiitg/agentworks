import{_ as p,g as j,s as q,a as Z,b as H,q as J,p as K,l as F,c as Q,E as X,I as Y,P as ee,d as te,y as ae,G as re}from"./mermaid.core-B-pPO8vB.js";import{p as ne}from"./chunk-4BX2VUAB-Bd7ivd0T.js";import{p as ie}from"./treemap-KMMF4GRG-D2RHWFUK.js";import"./transform-CtP_ysoj.js";import{d as R}from"./arc-Dr8D-heX.js";import{bi as S,bl as z,da as se,db as le}from"./index-BpZBU6kh.js";import"./min-wnpQj-tW.js";import"./_baseUniq-d9_9lcqt.js";function oe(e,a){return a<e?-1:a>e?1:a>=e?0:NaN}function ce(e){return e}function ue(){var e=ce,a=oe,f=null,y=S(0),s=S(z),o=S(0);function l(t){var n,c=(t=se(t)).length,d,x,m=0,u=new Array(c),i=new Array(c),v=+y.apply(this,arguments),w=Math.min(z,Math.max(-z,s.apply(this,arguments)-v)),h,C=Math.min(Math.abs(w)/c,o.apply(this,arguments)),$=C*(w<0?-1:1),g;for(n=0;n<c;++n)(g=i[u[n]=n]=+e(t[n],n,t))>0&&(m+=g);for(a!=null?u.sort(function(A,D){return a(i[A],i[D])}):f!=null&&u.sort(function(A,D){return f(t[A],t[D])}),n=0,x=m?(w-c*$)/m:0;n<c;++n,v=h)d=u[n],g=i[d],h=v+(g>0?g*x:0)+$,i[d]={data:t[d],index:n,value:g,startAngle:v,endAngle:h,padAngle:C};return i}return l.value=function(t){return arguments.length?(e=typeof t=="function"?t:S(+t),l):e},l.sortValues=function(t){return arguments.length?(a=t,f=null,l):a},l.sort=function(t){return arguments.length?(f=t,a=null,l):f},l.startAngle=function(t){return arguments.length?(y=typeof t=="function"?t:S(+t),l):y},l.endAngle=function(t){return arguments.length?(s=typeof t=="function"?t:S(+t),l):s},l.padAngle=function(t){return arguments.length?(o=typeof t=="function"?t:S(+t),l):o},l}var pe=re.pie,G={sections:new Map,showData:!1},T=G.sections,N=G.showData,de=structuredClone(pe),ge=p(()=>structuredClone(de),"getConfig"),fe=p(()=>{T=new Map,N=G.showData,ae()},"clear"),he=p(({label:e,value:a})=>{if(a<0)throw new Error(`"${e}" has invalid value: ${a}. Negative values are not allowed in pie charts. All slice values must be >= 0.`);T.has(e)||(T.set(e,a),F.debug(`added new section: ${e}, with value: ${a}`))},"addSection"),me=p(()=>T,"getSections"),ve=p(e=>{N=e},"setShowData"),Se=p(()=>N,"getShowData"),L={getConfig:ge,clear:fe,setDiagramTitle:K,getDiagramTitle:J,setAccTitle:H,getAccTitle:Z,setAccDescription:q,getAccDescription:j,addSection:he,getSections:me,setShowData:ve,getShowData:Se},ye=p((e,a)=>{ne(e,a),a.setShowData(e.showData),e.sections.map(a.addSection)},"populateDb"),xe={parse:p(async e=>{const a=await ie("pie",e);F.debug(a),ye(a,L)},"parse")},we=p(e=>`
  .pieCircle{
    stroke: ${e.pieStrokeColor};
    stroke-width : ${e.pieStrokeWidth};
    opacity : ${e.pieOpacity};
  }
  .pieOuterCircle{
    stroke: ${e.pieOuterStrokeColor};
    stroke-width: ${e.pieOuterStrokeWidth};
    fill: none;
  }
  .pieTitleText {
    text-anchor: middle;
    font-size: ${e.pieTitleTextSize};
    fill: ${e.pieTitleTextColor};
    font-family: ${e.fontFamily};
  }
  .slice {
    font-family: ${e.fontFamily};
    fill: ${e.pieSectionTextColor};
    font-size:${e.pieSectionTextSize};
    // fill: white;
  }
  .legend text {
    fill: ${e.pieLegendTextColor};
    font-family: ${e.fontFamily};
    font-size: ${e.pieLegendTextSize};
  }
`,"getStyles"),Ae=we,De=p(e=>{const a=[...e.values()].reduce((s,o)=>s+o,0),f=[...e.entries()].map(([s,o])=>({label:s,value:o})).filter(s=>s.value/a*100>=1).sort((s,o)=>o.value-s.value);return ue().value(s=>s.value)(f)},"createPieArcs"),Ce=p((e,a,f,y)=>{F.debug(`rendering pie chart
`+e);const s=y.db,o=Q(),l=X(s.getConfig(),o.pie),t=40,n=18,c=4,d=450,x=d,m=Y(a),u=m.append("g");u.attr("transform","translate("+x/2+","+d/2+")");const{themeVariables:i}=o;let[v]=ee(i.pieOuterStrokeWidth);v??=2;const w=l.textPosition,h=Math.min(x,d)/2-t,C=R().innerRadius(0).outerRadius(h),$=R().innerRadius(h*w).outerRadius(h*w);u.append("circle").attr("cx",0).attr("cy",0).attr("r",h+v/2).attr("class","pieOuterCircle");const g=s.getSections(),A=De(g),D=[i.pie1,i.pie2,i.pie3,i.pie4,i.pie5,i.pie6,i.pie7,i.pie8,i.pie9,i.pie10,i.pie11,i.pie12];let b=0;g.forEach(r=>{b+=r});const P=A.filter(r=>(r.data.value/b*100).toFixed(0)!=="0"),E=le(D);u.selectAll("mySlices").data(P).enter().append("path").attr("d",C).attr("fill",r=>E(r.data.label)).attr("class","pieCircle"),u.selectAll("mySlices").data(P).enter().append("text").text(r=>(r.data.value/b*100).toFixed(0)+"%").attr("transform",r=>"translate("+$.centroid(r)+")").style("text-anchor","middle").attr("class","slice"),u.append("text").text(s.getDiagramTitle()).attr("x",0).attr("y",-400/2).attr("class","pieTitleText");const W=[...g.entries()].map(([r,M])=>({label:r,value:M})),k=u.selectAll(".legend").data(W).enter().append("g").attr("class","legend").attr("transform",(r,M)=>{const O=n+c,B=O*W.length/2,V=12*n,U=M*O-B;return"translate("+V+","+U+")"});k.append("rect").attr("width",n).attr("height",n).style("fill",r=>E(r.label)).style("stroke",r=>E(r.label)),k.append("text").attr("x",n+c).attr("y",n-c).text(r=>s.getShowData()?`${r.label} [${r.value}]`:r.label);const _=Math.max(...k.selectAll("text").nodes().map(r=>r?.getBoundingClientRect().width??0)),I=x+t+n+c+_;m.attr("viewBox",`0 0 ${I} ${d}`),te(m,d,I,l.useMaxWidth)},"draw"),$e={draw:Ce},Ne={parser:xe,db:L,renderer:$e,styles:Ae};export{Ne as diagram};
