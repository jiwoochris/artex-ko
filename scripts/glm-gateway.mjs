import {createServer} from 'node:http';
import {timingSafeEqual} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {Readable} from 'node:stream';
import {pipeline} from 'node:stream/promises';
import {pathToFileURL} from 'node:url';
const endpoint='https://api.z.ai/api/coding/paas/v4/chat/completions';
const fields=new Set(['model','messages','stream','stream_options','temperature','top_p','max_tokens','response_format','stop','frequency_penalty','presence_penalty','seed','tools','tool_choice','parallel_tool_calls','thinking']);
export function gateway({key,getKey,fetchUpstream=fetch}) {
 const expected=Buffer.from('Bearer '+key);if(key.length<32)throw Error('Invalid key');let active=0,times=[];
 const reply=(res,status,message)=>{res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify({error:{message}}));};
 return createServer(async(req,res)=>{
  const given=Buffer.from(req.headers.authorization??'');
  if(given.length!==expected.length||!timingSafeEqual(given,expected))return reply(res,401,'Authentication required');
  if(req.headers.origin)return reply(res,403,'Browser requests forbidden');
  if(req.method!=='POST'||req.url!=='/v1/chat/completions')return reply(res,404,'Route forbidden');
  times=times.filter(t=>t>Date.now()-60000);if(active>=2||times.length>=30)return reply(res,429,'Request limit');
  times.push(Date.now());active++;
  const ac=new AbortController(),timer=setTimeout(()=>ac.abort(),120000);res.on('close',()=>{if(!res.writableFinished)ac.abort();});
  try {
   const chunks=[];let size=0;
   for await(const part of req.iterator({destroyOnReturn:false})){size+=part.length;if(size>2*1024*1024){req.resume();return reply(res,413,'Request too large');}chunks.push(part);}
   let b;try{b=JSON.parse(Buffer.concat(chunks));}catch{return reply(res,400,'Invalid JSON');}
   if(!b||typeof b!=='object'||Object.keys(b).some(k=>!fields.has(k))||b.model!=='glm-5.3'||!Array.isArray(b.messages)||b.messages.some(m=>!m||!['system','user','assistant','tool'].includes(m.role)||(typeof m.content!=='string'&&!(m.role==='assistant'&&m.content==null&&Array.isArray(m.tool_calls)))||(m.role==='tool'&&typeof m.tool_call_id!=='string')||(m.tool_calls!==undefined&&(!Array.isArray(m.tool_calls)||m.tool_calls.some(t=>!t||t.type!=='function'||typeof t.id!=='string'||typeof t.function?.name!=='string'||typeof t.function?.arguments!=='string'))))||b.stream!==undefined&&typeof b.stream!=='boolean')return reply(res,400,'Unsupported request');
   if(b.tools!==undefined&&(!Array.isArray(b.tools)||b.tools.some(t=>!t||t.type!=='function'||typeof t.function?.name!=='string')))return reply(res,400,'Only local function tools supported');
   if(b.max_tokens!==undefined&&(!Number.isInteger(b.max_tokens)||b.max_tokens<1))return reply(res,400,'Invalid output limit');
   b.max_tokens=Math.min(b.max_tokens??16384,16384);
   const up=await fetchUpstream(endpoint,{method:'POST',redirect:'error',signal:ac.signal,headers:{authorization:'Bearer '+await getKey(),'content-type':'application/json'},body:JSON.stringify(b)});
   if(!up.ok){await up.body?.cancel();return reply(res,up.status,'z.ai request failed ('+up.status+')');}
   res.writeHead(200,{'content-type':up.headers.get('content-type')??'application/json','cache-control':'no-store','x-accel-buffering':'no'});
   await pipeline(Readable.fromWeb(up.body),res,{signal:ac.signal});
  }catch{if(res.headersSent||res.destroyed)res.destroy();else reply(res,502,'Provider request failed');}
  finally{clearTimeout(timer);active--;}
 });
}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
 try{
  const c=JSON.parse(await readFile(process.argv[2],'utf8'));
  const getKey=async()=>{const pairs=(await readFile(c.credentialsFile,'utf8')).split('\n').filter(l=>/^BM_AGENT_LLM_/.test(l)).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim()];});const x=Object.fromEntries(pairs);if(x.BM_AGENT_LLM_BASE_URL!=='https://api.z.ai/api/coding/paas/v4'||!x.BM_AGENT_LLM_API_KEY)throw Error('Invalid provider configuration');return x.BM_AGENT_LLM_API_KEY;};
  const s=gateway({key:c.key,getKey});s.requestTimeout=15000;s.headersTimeout=10000;s.listen(18789,'127.0.0.1',()=>console.log('ARTEX GLM gateway ready'));
  s.on('error',()=>{console.error('Gateway listener failed');process.exitCode=1;});
  for(const sig of ['SIGINT','SIGTERM'])process.on(sig,()=>{s.close();s.closeAllConnections();});
 }catch{console.error('Gateway startup failed');process.exitCode=1;}
}
