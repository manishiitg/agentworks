export interface OrbitHUD {
   score:number;
  wave:number;
  hp:number;
  novas:number;
  paused:boolean;
  playing:boolean;
  gameOver:boolean;
  announcement:string
}
export interface OrbitController {
   play:(rush:boolean)=>void;
  ambient:()=>void;
  nova:()=>void;
  pause:()=>void;
  destroy:()=>void
}
type Star={
  x:number;
  y:number;
  z:number;
  r:number;
  a:number
}
;
type Particle={
  x:number;
  y:number;
  vx:number;
  vy:number;
  life:number;
  max:number;
  r:number;
  col:string
}
;
type Bullet={
  x:number;
  y:number;
  vx:number;
  vy:number;
  life:number;
  r:number;
  col:string
}
;
type Enemy={
  x:number;
  y:number;
  v:number;
  hp:number;
  age:number;
  kind:number
}
;
type Ring={
  x:number;
  y:number;
  r:number;
  life:number;
  col:string
}
;
type Boss={
  x:number;
  y:number;
  hp:number;
  max:number;
  phase:number;
  fire:number;
  age:number
}
;
/** Shipped canvas renderer only: themes cannot supply executable code.
 * Bounds: 650 particles, 360 bullets per side, 26 ships, DPR <= 1.5.
 * Ambient 30fps; pause/death/hidden stop RAF; listeners belong to this canvas.
 */
export function createNeonOrbit(canvas:HTMLCanvasElement,onHUD:(state:OrbitHUD)=>void):OrbitController {
  const context=canvas.getContext('2d',{
    alpha:false
  }
  );
  if (!context) throw new Error('Canvas is unavailable on this device.');
  const ctx: CanvasRenderingContext2D=context;
  let W=innerWidth,H=innerHeight,dpr=1,t=0,last=0,frame=0,acc=0,paused=matchMedia('(prefers-reduced-motion: reduce)').matches,gentle=paused,playing=false,bossRush=false,intensity=.8,palette: keyof typeof colors='mint',score=0,wave=1,kills=0,spawnClock=0,shootClock=0,bossClock=0,bannerUntil=0,shake=0,dead=false,boss:Boss|null=null,novaWave:{
    x:number;
    y:number;
    r:number
  }
  |null=null,announcement='';
  const keys=new Set<string>(),stars:Star[]=[],particles:Particle[]=[],bullets:Bullet[]=[],hostiles:Bullet[]=[],enemies:Enemy[]=[],rings:Ring[]=[];
  const colors={
    mint:{
      main:'#9bffd9',secondary:'#62adff',fog:'#124b5e'
    }
    ,violet:{
      main:'#e3a6ff',secondary:'#a17aff',fog:'#432271'
    }
    ,solar:{
      main:'#ffd19b',secondary:'#ff778f',fog:'#742b38'
    }
  }
  ;
  let player={
    x:W*.55,y:H*.79,tx:W*.55,ty:H*.79,hp:5,invincible:0,dash:0,dashCD:0,novas:3
  }
  ,mouse=false;
  const rand=(a:number,b:number)=>a+Math.random()*(b-a),clamp=(x:number,a:number,b:number)=>Math.max(a,Math.min(b,x)),dist=(a:{
    x:number;
    y:number
  }
  ,b:{
    x:number;
    y:number
  }
  )=>Math.hypot(a.x-b.x,a.y-b.y),C=()=>colors[palette];
  function fit(){
    W=innerWidth;
    H=innerHeight;
    dpr=Math.min(devicePixelRatio||1,1.5);
    canvas.width=Math.round(W*dpr);
    canvas.height=Math.round(H*dpr);
    ctx.setTransform(dpr,0,0,dpr,0,0);
    player.x=clamp(player.x,20,W-20);
    player.y=clamp(player.y,80,H-25);
    player.tx=player.x;
    player.ty=player.y;
    stars.length=0;
    for(let i=0;
    i<170;
    i++)stars.push({
      x:rand(0,W),y:rand(0,H),z:rand(.2,1.4),r:rand(.3,1.4),a:rand(.2,.8)
    }
    );
    resumeLoop();
  }
  window.addEventListener('resize',fit);
  fit();
  function reset(rush:boolean){
    playing=true;
    bossRush=rush;
    dead=false;
    score=0;
    wave=1;
    kills=0;
    boss=null;
    spawnClock=0;
    shootClock=0;
    bossClock=0;
    bullets.length=hostiles.length=enemies.length=particles.length=rings.length=0;
    player={
      x:W*.55,y:H*.79,tx:W*.55,ty:H*.79,hp:5,invincible:2,dash:0,dashCD:0,novas:3
    }
    ;
    updateHud();
    selectMode(rush?'bossRush':'pilot');
    if(paused)setPause(false);
    if(rush)spawnBoss();
    else announce('SYSTEM ONLINE','ENTER THE ORBIT');
    canvas.focus({
      preventScroll:true
    }
    );
    resumeLoop();
  }
  function ambient(){
    playing=false;
    dead=false;
    bossRush=false;
    boss=null;
    enemies.length=bullets.length=hostiles.length=0;
    announcement='';
    selectMode('ambient');
    resumeLoop();
  }
  function selectMode(_id: string){
    emit();
  }
  function emit(){
    onHUD({
      score,wave,hp:player.hp,novas:player.novas,paused,playing,gameOver:dead,announcement
    }
    );
  }
  function updateHud(){
    emit();
  }
  function announce(s:string,h:string){
    announcement=s+' · '+h;
    bannerUntil=t+2.4;
    emit();
  }
  function setPause(p:boolean){
    paused=p;
    last=0;
    emit();
    resumeLoop();
  }
  function puff(x:number,y:number,col:string,n=20,speed=150){
    for(let i=0;
    i<n&&particles.length<650;
    i++){
      const a=rand(0,Math.PI*2),v=rand(20,speed);
      particles.push({
        x,y,vx:Math.cos(a)*v,vy:Math.sin(a)*v,life:rand(.3,.9),max:1,r:rand(1,3),col
      }
      );
    }
  }
  function ring(x:number,y:number,col:string){
    if(rings.length<14)rings.push({
      x,y,r:4,life:1,col
    }
    );
  }
  function fire(x:number,y:number,a:number,speed:number,col:string,type='enemy',r=3){
    const arr=type==='player'?bullets:hostiles;
    if(arr.length<360)arr.push({
      x,y,vx:Math.cos(a)*speed,vy:Math.sin(a)*speed,col,r,life:7
    }
    );
  }
  function nova(){
    if(!playing||dead||paused||player.novas<=0)return;
    player.novas--;
    novaWave={
      x:player.x,y:player.y,r:0
    }
    ;
    hostiles.length=0;
    for(const e of enemies){
      puff(e.x,e.y,C().main,12);
      score+=100;
    }
    enemies.length=0;
    if(boss){
      boss.hp=Math.max(1,boss.hp-95);
      ring(boss.x,boss.y,'#e6aaff');
    }
    player.invincible=2;
    updateHud();
  }
  function dash(){
    if(!playing||dead||paused||player.dashCD>0)return;
    player.dash=.25;
    player.invincible=.6;
    player.dashCD=2;
    ring(player.x,player.y,C().main);
  }
  function spawnBoss(){
    boss={
      x:W*.58,y:-110,hp:280+wave*55,max:280+wave*55,phase:0,fire:0,age:0
    }
    ;
    announce('GRAVITY ANOMALY','NULL ENGINE');
    enemies.length=0;
  }
  function hit(){
    if(player.invincible>0)return;
    player.hp--;
    player.invincible=1.7;
    if(!gentle)shake=7;
    puff(player.x,player.y,'#ff9aaf',25);
    updateHud();
    if(player.hp<=0){
      dead=true;
      emit();
    }
  }
  function update(dt:number){
    t+=dt;
    if(bannerUntil&&t>bannerUntil){
      announcement='';
      bannerUntil=0;
      emit();
    }
    for(const s of stars){
      s.y+=dt*s.z*(playing?26:8);
      if(s.y>H){
        s.y=0;
        s.x=rand(0,W);
      }
    }
    for(let i=particles.length-1;
    i>=0;
    i--){
      const p=particles[i];
      p.x+=p.vx*dt;
      p.y+=p.vy*dt;
      p.vx*=1-dt;
      p.vy*=1-dt;
      p.life-=dt;
      if(p.life<=0)particles.splice(i,1);
    }
    for(let i=rings.length-1;
    i>=0;
    i--){
      rings[i].r+=dt*180;
      rings[i].life-=dt;
      if(rings[i].life<=0)rings.splice(i,1);
    }
    if(novaWave){
      novaWave.r+=dt*1200;
      if(novaWave.r>Math.max(W,H)*1.2)novaWave=null;
    }
    if(!playing){
      player.x=W*.55+Math.sin(t*.22)*W*.16;
      player.y=H*.72+Math.cos(t*.4)*H*.06;
      shootClock-=dt;
      if(shootClock<0){
        fire(player.x-8,player.y-15,-Math.PI/2,520,C().main,'player');
        fire(player.x+8,player.y-15,-Math.PI/2,520,C().main,'player');
        shootClock=.5;
      }
      spawnClock-=dt;
      if(spawnClock<0&&enemies.length<9){
        enemies.push({
          x:rand(W*.28,W*.8),y:-30,v:rand(30,65),hp:2,age:rand(0,6),kind:Math.random()>.6?1:0
        }
        );
        spawnClock=1.1;
      }
    }
    if(playing&&!dead){
      player.invincible-=dt;
      player.dashCD=Math.max(0,player.dashCD-dt);
      player.dash=Math.max(0,player.dash-dt);
      let dx=(keys.has('d')||keys.has('ArrowRight')?1:0)-(keys.has('a')||keys.has('ArrowLeft')?1:0),dy=(keys.has('s')||keys.has('ArrowDown')?1:0)-(keys.has('w')||keys.has('ArrowUp')?1:0);
      if(dx||dy){
        const m=Math.hypot(dx,dy);
        player.x+=dx/m*dt*(player.dash>0?1000:390);
        player.y+=dy/m*dt*(player.dash>0?1000:390);
        player.tx=player.x;
        player.ty=player.y;
        mouse=false;
      }
      else if(mouse){
        const f=1-Math.exp(-dt*(player.dash>0?22:11));
        player.x+=(player.tx-player.x)*f;
        player.y+=(player.ty-player.y)*f;
      }
      player.x=clamp(player.x,18,W-18);
      player.y=clamp(player.y,78,H-24);
      shootClock-=dt;
      if(shootClock<=0){
        let n=wave>=3?3:2;
        for(let i=0;
        i<n;
        i++)fire(player.x+(i-(n-1)/2)*12,player.y-16,-Math.PI/2+(i-(n-1)/2)*.04,650,C().main,'player');
        shootClock=.12;
      }
      spawnClock-=dt;
      if(!boss&&spawnClock<=0&&enemies.length<26){
        const x=rand(W*.18,W*.87);
        enemies.push({
          x,y:-30,v:rand(60,110)+wave*7,hp:wave>=3?3:2,age:rand(0,6),kind:Math.random()>.65?1:0
        }
        );
        spawnClock=Math.max(.25,.85-wave*.05);
      }
      if(!boss&&kills>=9+wave*2){
        spawnBoss();
        kills=0;
      }
      if(boss){
        boss.age+=dt;
        boss.x=W*.52+Math.sin(boss.age*.6)*W*.18;
        boss.y+=(Math.min(H*.23,180)-boss.y)*(1-Math.exp(-dt*1.4));
        boss.fire-=dt;
        if(boss.fire<=0&&boss.age>1.3){
          let n=14+Math.min(wave,7)*2;
          const angle=boss.age*.85;
          for(let i=0;
          i<n;
          i++){
            const a=angle+i*Math.PI*2/n;
            fire(boss.x,boss.y,a,105+wave*10,'#e0a4ff');
          }
          for(let j=-1;
          j<=1;
          j++){
            const a=Math.atan2(player.y-boss.y,player.x-boss.x)+j*.16;
            fire(boss.x,boss.y,a,185+wave*8,C().secondary,'enemy',4);
          }
          boss.fire=.68;
        }
      }
    }
    for(let i=enemies.length-1;
    i>=0;
    i--){
      const e=enemies[i];
      e.age+=dt;
      e.y+=e.v*dt;
      e.x+=Math.sin(e.age*1.7)*dt*33;
      if(e.y>H+30){
        enemies.splice(i,1);
        continue;
      }
      if(playing&&!dead&&e.age%2.2<dt&&e.y>30&&e.y<H*.7){
        const a=Math.atan2(player.y-e.y,player.x-e.x);
        fire(e.x,e.y,a,150,C().secondary);
      }
      if(playing&&!dead&&dist(e,player)<24){
        hit();
        puff(e.x,e.y,C().secondary,15);
        enemies.splice(i,1);
      }
    }
    for(const arr of [bullets,hostiles])for(let i=arr.length-1;
    i>=0;
    i--){
      const b=arr[i];
      b.x+=b.vx*dt;
      b.y+=b.vy*dt;
      b.life-=dt;
      if(b.x< -40||b.x>W+40||b.y< -50||b.y>H+50||b.life<0){
        arr.splice(i,1);
        continue;
      }
      if(arr===hostiles){
        if(playing&&!dead&&dist(b,player)<b.r+7){
          arr.splice(i,1);
          hit();
        }
        continue;
      }
      let gone=false;
      for(let j=enemies.length-1;
      j>=0;
      j--){
        const e=enemies[j];
        if(dist(b,e)<20){
          e.hp--;
          arr.splice(i,1);
          gone=true;
          puff(b.x,b.y,C().main,3,60);
          if(e.hp<=0){
            puff(e.x,e.y,C().secondary,16);
            enemies.splice(j,1);
            if(playing){
              score+=120;
              kills++;
              updateHud();
            }
          }
          break;
        }
      }
      if(!gone&&boss&&Math.hypot(b.x-boss.x,(b.y-boss.y)*1.5)<77){
        boss.hp-=3;
        arr.splice(i,1);
        puff(b.x,b.y,'#e5a4ff',2,50);
        if(boss.hp<=0){
          puff(boss.x,boss.y,'#e5a4ff',90,320);
          ring(boss.x,boss.y,C().main);
          hostiles.length=0;
          boss=null;
          score+=2500;
          wave++;
          player.hp=Math.min(5,player.hp+1);
          player.novas=Math.min(5,player.novas+1);
          updateHud();
          announce('ENGINE DESTROYED','ORBIT RESTORED');
          if(bossRush){
            spawnClock=6;
            bossClock=4;
          }
        }
      }
    }
    if(playing&&bossRush&&!boss&&bossClock>0){
      bossClock-=dt;
      if(bossClock<=0)spawnBoss();
    }
    shake*=Math.max(0,1-dt*12);
  }
  function glow(col:string,blur=15){
    ctx.shadowColor=col;
    ctx.shadowBlur=gentle?5:blur*intensity;
  }
  function ship(x:number,y:number,size:number,col:string,enemy=false,angle=0){
    ctx.save();
    ctx.translate(x,y);
    ctx.rotate(angle);
    glow(col,15);
    ctx.strokeStyle=col;
    ctx.fillStyle=enemy?'#16243b':'#0b2f36';
    ctx.lineWidth=1.3;
    ctx.beginPath();
    if(enemy){
      ctx.moveTo(0,size);
      ctx.lineTo(size*1.2,-size*.6);
      ctx.lineTo(size*.35,-size*.2);
      ctx.lineTo(0,-size);
      ctx.lineTo(-size*.35,-size*.2);
      ctx.lineTo(-size*1.2,-size*.6);
    }
    else{
      ctx.moveTo(0,-size*1.2);
      ctx.lineTo(size,size*.9);
      ctx.lineTo(0,size*.5);
      ctx.lineTo(-size,size*.9);
    }
    ctx.closePath();
    ctx.fill();
    ctx.stroke();
    if(!enemy){
      ctx.fillStyle='#ddfff3';
      ctx.beginPath();
      ctx.moveTo(-3,size*.8);
      ctx.lineTo(0,size*(1.7+Math.sin(t*40)*.3));
      ctx.lineTo(3,size*.8);
      ctx.fill();
      ctx.fillStyle=col;
      ctx.fillRect(-1.5,-size*.4,3,size*.6);
    }
    ctx.restore();
  }
  function draw(){
    const c=C();
    ctx.setTransform(dpr,0,0,dpr,0,0);
    ctx.fillStyle='#040b14';
    ctx.fillRect(0,0,W,H);
    const fog=ctx.createRadialGradient(W*.63,H*.4,0,W*.63,H*.4,W*.62);
    fog.addColorStop(0,c.fog+'aa');
    fog.addColorStop(.4,c.fog+'38');
    fog.addColorStop(1,'#040b1400');
    ctx.fillStyle=fog;
    ctx.fillRect(0,0,W,H);
    const fog2=ctx.createRadialGradient(W*.8,H*.82,0,W*.8,H*.82,W*.4);
    fog2.addColorStop(0,palette==='solar'?'#65222c66':'#251a5e70');
    fog2.addColorStop(1,'#0000');
    ctx.fillStyle=fog2;
    ctx.fillRect(0,0,W,H);
    for(const s of stars){
      ctx.globalAlpha=s.a*(.7+.3*Math.sin(t*.5+s.x))*intensity;
      ctx.fillStyle=s.z>1?c.main:'#b5ced8';
      ctx.fillRect(s.x,s.y,s.r,s.r);
    }
    ctx.globalAlpha=1;
    // The orbital gate is a dimensional backdrop, not a video or an external image.
const cx=W*.62,cy=H*.43,R=Math.min(W*.25,H*.39);
    ctx.save();
    const core=ctx.createRadialGradient(cx-R*.07,cy-R*.08,0,cx,cy,R*.29);
    core.addColorStop(0,'#bce9ef38');
    core.addColorStop(.6,c.fog+'c0');
    core.addColorStop(1,'#030a15');
    ctx.fillStyle=core;
    ctx.beginPath();
    ctx.arc(cx,cy,R*.29,0,Math.PI*2);
    ctx.fill();
    ctx.strokeStyle=c.main+'22';
    ctx.stroke();
    ctx.translate(cx,cy);
    ctx.rotate(-.3);
    for(let j=0;
    j<4;
    j++){
      ctx.beginPath();
      ctx.ellipse(0,0,R+j*9,R*.32+j*5,0,0,Math.PI*2);
      ctx.strokeStyle=j===0?c.main+'50':c.secondary+'18';
      ctx.lineWidth=j===0?1.3:.6;
      glow(c.main,j===0?22:0);
      ctx.stroke();
    }
    ctx.shadowBlur=0;
    for(let i=0;
    i<40;
    i++){
      let a=i*Math.PI/20+t*(gentle?.015:.065);
      ctx.fillStyle=i%4?c.main+'38':c.main+'a0';
      ctx.save();
      ctx.translate(Math.cos(a)*(R+19),Math.sin(a)*(R*.32+15));
      ctx.rotate(a);
      ctx.fillRect(0,0,i%4?2:8,1);
      ctx.restore();
    }
    ctx.restore();
    // Perspective star grid.
ctx.save();
    ctx.strokeStyle=c.main+'12';
    ctx.lineWidth=.7;
    const horizon=H*.72;
    for(let i=-10;
    i<=10;
    i++){
      ctx.beginPath();
      ctx.moveTo(W*.6+i*17,horizon);
      ctx.lineTo(W*.6+i*W*.17,H);
      ctx.stroke();
    }
    for(let i=0;
    i<10;
    i++){
      let f=((i/10+t*(gentle?.01:.035))%1);
      let y=horizon+(H-horizon)*f*f;
      ctx.beginPath();
      ctx.moveTo(0,y);
      ctx.lineTo(W,y);
      ctx.stroke();
    }
    ctx.restore();
    if(shake>.3&&!gentle){
      ctx.translate(rand(-shake,shake),rand(-shake,shake));
    }
    for(const e of enemies)ship(e.x,e.y,e.kind?15:11,e.kind?'#dba1ff':c.secondary,true,Math.sin(e.age)*.08);
    if(boss){
      ctx.save();
      ctx.font='10px ui-monospace, monospace';
      ctx.textAlign='center';
      ctx.fillStyle='#e4b9ee';
      ctx.fillText('THE NULL ENGINE',W*.55,100);
      ctx.fillStyle='#e4b9ee44';
      ctx.fillRect(W*.35,110,W*.4,3);
      ctx.fillStyle='#e4b9ee';
      ctx.fillRect(W*.35,110,W*.4*Math.max(0,boss.hp/boss.max),3);
      ctx.restore();
      ctx.save();
      ctx.translate(boss.x,boss.y);
      glow('#dd9eff',22);
      ctx.strokeStyle='#d6a0ff';
      ctx.fillStyle='#191331';
      ctx.lineWidth=2;
      ctx.beginPath();
      for(let i=0;
      i<8;
      i++){
        const a=i*Math.PI/4;
        const r=i%2?37:77;
        const x=Math.cos(a)*r,y=Math.sin(a)*r*.6;
        i?ctx.lineTo(x,y):ctx.moveTo(x,y);
      }
      ctx.closePath();
      ctx.fill();
      ctx.stroke();
      ctx.rotate(t*.6);
      ctx.beginPath();
      ctx.arc(0,0,22,0,Math.PI*2);
      ctx.strokeStyle=C().main;
      ctx.stroke();
      for(let i=0;
      i<3;
      i++){
        ctx.rotate(Math.PI*2/3);
        ctx.fillStyle='#fff5ff';
        ctx.fillRect(17,-2,10,4);
      }
      ctx.restore();
    }
    for(const b of bullets){
      ctx.save();
      glow(b.col,12);
      ctx.strokeStyle=b.col;
      ctx.lineWidth=2;
      ctx.beginPath();
      ctx.moveTo(b.x,b.y);
      ctx.lineTo(b.x-b.vx*.018,b.y-b.vy*.018);
      ctx.stroke();
      ctx.restore();
    }
    for(const b of hostiles){
      ctx.save();
      glow(b.col,10);
      ctx.fillStyle=b.col;
      ctx.beginPath();
      ctx.arc(b.x,b.y,b.r,0,Math.PI*2);
      ctx.fill();
      ctx.restore();
    }
    for(const p of particles){
      ctx.save();
      ctx.globalAlpha=clamp(p.life,0,1);
      ctx.fillStyle=p.col;
      ctx.fillRect(p.x,p.y,p.r,p.r);
      ctx.restore();
    }
    for(const r of rings){
      ctx.save();
      ctx.strokeStyle=r.col;
      ctx.globalAlpha=r.life*.5;
      ctx.lineWidth=1;
      ctx.beginPath();
      ctx.arc(r.x,r.y,r.r,0,Math.PI*2);
      ctx.stroke();
      ctx.restore();
    }
    if(novaWave){
      ctx.save();
      ctx.strokeStyle=c.main;
      ctx.lineWidth=3;
      ctx.globalAlpha=Math.max(0,1-novaWave.r/Math.max(W,H));
      glow(c.main,25);
      ctx.beginPath();
      ctx.arc(novaWave.x,novaWave.y,novaWave.r,0,Math.PI*2);
      ctx.stroke();
      ctx.restore();
    }
    if(!dead){
      ctx.save();
      ctx.globalAlpha=player.invincible>0?.65+Math.sin(t*10)*.15:1;
      ship(player.x,player.y,15,c.main);
      if(player.invincible>0){
        ctx.strokeStyle=c.main+'70';
        ctx.lineWidth=1;
        ctx.beginPath();
        ctx.arc(player.x,player.y,25,0,Math.PI*2);
        ctx.stroke();
      }
      ctx.restore();
    }
  }
  function loop(now:number){
    frame=0;
    if(document.hidden)return;
    if(paused||dead){
      draw();
      return;
    }
    const dt=last?Math.min((now-last)/1000,.04):.016;
    last=now;
    acc+=dt;
    const interval=playing&&!gentle?1/60:1/30;
    if(acc>=interval){
      if(!paused&&!dead)update(Math.min(acc,.06));
      draw();
      acc=0;
    }
    frame=requestAnimationFrame(loop);
  }
  function resumeLoop(){
    last=0;
    if(!frame&&!document.hidden)frame=requestAnimationFrame(loop);
  }
  const visibility=()=>{
    if(document.hidden){
      cancelAnimationFrame(frame);
      frame=0;
      keys.clear();
    }
    else resumeLoop();
  }
  ;
  const move=(e:PointerEvent)=>{
    if(playing&&!dead){
      player.tx=e.clientX;
      player.ty=e.clientY;
      mouse=true;
    }
  }
  ;
  const down=(e:PointerEvent)=>{
    if(playing&&!dead){
      canvas.setPointerCapture(e.pointerId);
      move(e);
      canvas.focus({
        preventScroll:true
      }
      );
    }
  }
  ;
  const keydown=(e:KeyboardEvent)=>{
     if(!playing)return;
     if(e.key==='Escape'){
      e.preventDefault();
      e.stopPropagation();
      setPause(!paused);
      return;
    }
     const k=e.key.length===1?e.key.toLowerCase():e.key;
     if(['ArrowUp','ArrowDown','ArrowLeft','ArrowRight',' ','Shift','w','a','s','d'].includes(k)){
      e.preventDefault();
      e.stopPropagation();
      keys.add(k);
      if(k===' '&&!e.repeat)nova();
      if(k==='Shift'&&!e.repeat)dash();
    }
  }
  ;
  const keyup=(e:KeyboardEvent)=>keys.delete(e.key.length===1?e.key.toLowerCase():e.key);
  const blur=()=>keys.clear();
  document.addEventListener('visibilitychange',visibility);
  canvas.addEventListener('pointermove',move);
  canvas.addEventListener('pointerdown',down);
  canvas.addEventListener('keydown',keydown);
  canvas.addEventListener('keyup',keyup);
  canvas.addEventListener('blur',blur);
  setPause(paused);
  resumeLoop();
  return {
     play:reset,ambient,nova,pause:()=>setPause(!paused),
     destroy:()=>{
      cancelAnimationFrame(frame);
      frame=0;
      window.removeEventListener('resize',fit);
      document.removeEventListener('visibilitychange',visibility);
      canvas.removeEventListener('pointermove',move);
      canvas.removeEventListener('pointerdown',down);
      canvas.removeEventListener('keydown',keydown);
      canvas.removeEventListener('keyup',keyup);
      canvas.removeEventListener('blur',blur);
      keys.clear();
    }
    ,
  }
  ;
}
