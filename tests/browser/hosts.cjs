const {chromium} = require('playwright');
const assert = require('node:assert/strict');
const {spawn,execFileSync} = require('node:child_process');
const net = require('node:net');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
(async()=>{
 const tmp=fs.mkdtempSync(path.join(os.tmpdir(),'mdserve-hosts-'));
 const binary=path.join(tmp,'mdserve');
 let browser;
 const children=[];
 const busy=net.createServer();
 try {
 fs.writeFileSync(path.join(tmp,'README.md'),'# Browser host gate\n\nordinary content');
 execFileSync('go',['build','-o',binary,'.'],{cwd:path.resolve(__dirname,'../..'),env:{...process.env,GOCACHE:process.env.GOCACHE||path.join(os.tmpdir(),'mdserve-go-cache')}});
 await new Promise(resolve=>busy.listen(0,'127.0.0.1',resolve));
 browser=await chromium.launch(process.env.MDSERVE_CHROME ? {executablePath:process.env.MDSERVE_CHROME}:{});
 for (const addr of ['127.0.0.1:0',`127.0.0.1:${busy.address().port}`,'[::1]:0',':0']) {
 const child=spawn(binary,['serve','--dir',tmp,'--addr',addr,'--no-update-check','--no-reload']);children.push(child);
 const url=await new Promise((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('startup timeout '+output)),10000);child.stdout.on('data',b=>{output+=b;const m=output.match(/http:\/\/[^\s]+\//);if(m){clearTimeout(timer);resolve(m[0]);}});child.on('exit',c=>reject(Error('server exit '+c)));});
 if(addr.includes(String(busy.address().port))) assert.notEqual(new URL(url).port,String(busy.address().port));
 const page=await browser.newPage();
 await page.goto(url);await page.waitForFunction(()=>document.querySelector('#page h1')?.textContent==='Browser host gate');
 assert.equal(await page.evaluate(async()=> (await fetch('/api/tree')).status),200);
 assert.equal(await page.evaluate(async()=> (await fetch('/raw?path=README.md',{method:'POST'})).status),405);
 const rejected=await page.request.get(url+'api/tree',{headers:{Host:'attacker.invalid:'+new URL(url).port}});assert.equal(rejected.status(),403);
 const foreign=await page.request.get(url+'raw?path=README.md',{headers:{Origin:'http://attacker.invalid'}});assert.equal(foreign.status(),403);
 await page.close();child.kill();console.log('PASS browser listener '+addr+' -> '+url);
 }
 }finally{children.forEach(c=>c.kill());busy.close();if(browser)await browser.close();fs.rmSync(tmp,{recursive:true,force:true});}
})().catch(e=>{console.error(e);process.exitCode=1});
