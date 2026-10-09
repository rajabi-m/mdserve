// Run rendering probes in a disposable process: a vulnerable diagram can block
// the renderer event loop, so a parent deadline must kill the entire browser.
const {spawn}=require('node:child_process');
const path=require('node:path');
const assert=require('node:assert/strict');
const probes=['gantt','math'];
if (!process.argv[2]) {
 (async()=>{
  for (const probe of probes) await new Promise((resolve,reject)=>{
   const child=spawn(process.execPath,[__filename,probe],{stdio:'inherit',detached:process.platform!=='win32'});
   const timer=setTimeout(()=>{
    if(process.platform==='win32') spawn('taskkill',['/pid',String(child.pid),'/t','/f']);
    else {try{process.kill(-child.pid,'SIGKILL');}catch{}}
    reject(Error(probe+' renderer exceeded 15-second deadline'));
   },15000);
   child.on('error',e=>{clearTimeout(timer);reject(e);});
   child.on('exit',code=>{clearTimeout(timer);code===0?resolve():reject(Error(probe+' probe failed: '+code));});
  });
 })().catch(e=>{console.error(e);process.exitCode=1;});
} else {
 const {chromium}=require('playwright');
 (async()=>{
  const browser=await chromium.launch(process.env.MDSERVE_CHROME?{executablePath:process.env.MDSERVE_CHROME}:{});
  try {
   const page=await browser.newPage();
   const assets=path.resolve(__dirname,'../../internal/web/assets');
   await page.setContent('<div id="diagram"></div>');
   for(const file of ['purify.min.js','mermaid.min.js','reader-security.js','katex.min.js'])await page.addScriptTag({path:path.join(assets,file)});
   if(process.argv[2]==='gantt') {
    await page.evaluate(async()=>{
     const node=document.querySelector('#diagram');
     node.textContent='gantt\n excludes monday,tuesday,wednesday,thursday,friday,saturday,sunday\n Dummy :2025-01-01, 1d';
     await mdserveSecurity.renderDiagrams([node],'default');
    });
    assert.ok(await page.locator('#diagram').textContent());
    console.log('PASS vendor Gantt all-days exclusion terminates');
   } else {
    const html=await page.evaluate(()=>{
     // KaTeX must ignore polluted inherited options even without an outer
     // sanitizer. No navigation or external resource is allowed in this probe.
     Object.prototype.trust=true;
     try{return katex.renderToString('\\href{javascript:window.__executed=true}{dummy}',{throwOnError:false});}
     finally{delete Object.prototype.trust;}
    });
    assert.ok(!html.includes('href="javascript:'),'inherited trust enabled executable link');
    console.log('PASS vendor KaTeX ignores inherited trust');
   }
  } finally {await browser.close();}
 })().catch(e=>{console.error(e);process.exitCode=1;});
}
