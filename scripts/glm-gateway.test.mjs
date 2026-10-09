import test from 'node:test';
import assert from 'node:assert/strict';
import {gateway} from './glm-gateway.mjs';
const key='x'.repeat(40);
test('local function calls and results pass through without executing on host',async()=>{
 let sent;
 const s=gateway({key,getKey:async()=> 'secret',fetchUpstream:async(url,opts)=>{sent=JSON.parse(opts.body);assert.equal(url,'https://api.z.ai/api/coding/paas/v4/chat/completions');return Response.json({choices:[{message:{content:'42'}}]});}});
 await new Promise(r=>s.listen(0,'127.0.0.1',r));
 try{
  const body={model:'glm-5.3',messages:[{role:'user',content:'add 17 and 25'},{role:'assistant',content:null,tool_calls:[{id:'c1',type:'function',function:{name:'add',arguments:'{"a":17,"b":25}'}}]},{role:'tool',tool_call_id:'c1',content:'42'}],tools:[{type:'function',function:{name:'add',parameters:{type:'object'}}}],max_tokens:99999};
  const res=await fetch(`http://127.0.0.1:${s.address().port}/v1/chat/completions`,{method:'POST',headers:{authorization:'Bearer '+key,'content-type':'application/json'},body:JSON.stringify(body)});
  assert.equal(res.status,200);assert.equal(sent.max_tokens,16384);assert.deepEqual(sent.messages,body.messages);assert.deepEqual(sent.tools,body.tools);
 }finally{s.closeAllConnections();await new Promise(r=>s.close(r));}
});
test('rejects unauthorized, external tools, images, alternate models and routes',async()=>{
 let calls=0;
 const s=gateway({key,getKey:async()=> 'secret',fetchUpstream:async()=>{calls++;return Response.json({});}});
 await new Promise(r=>s.listen(0,'127.0.0.1',r));
 const base={model:'glm-5.3',messages:[{role:'user',content:'OK'}]};
 try{
  const cases=[{auth:'bad',status:401},{origin:'https://example.com',status:403},{path:'/v1/responses',status:404},{body:{...base,model:'other'},status:400},{body:{...base,tools:[{type:'web_search'}]},status:400},{body:{...base,messages:[{role:'user',content:[{type:'image_url',image_url:{url:'http://localhost'}}]}]},status:400},{body:{...base,upstream:'https://example.com'},status:400}];
  for(const c of cases){const headers={authorization:'Bearer '+(c.auth??key),'content-type':'application/json'};if(c.origin)headers.origin=c.origin;const r=await fetch(`http://127.0.0.1:${s.address().port}${c.path??'/v1/chat/completions'}`,{method:'POST',headers,body:JSON.stringify(c.body??base)});assert.equal(r.status,c.status);await r.text();}
  assert.equal(calls,0);
 }finally{s.closeAllConnections();await new Promise(r=>s.close(r));}
});
test('upstream error body and credential never reach client',async()=>{
 const s=gateway({key,getKey:async()=> 'provider-secret',fetchUpstream:async()=>new Response('provider-secret sensitive body',{status:429})});
 await new Promise(r=>s.listen(0,'127.0.0.1',r));
 try{const r=await fetch(`http://127.0.0.1:${s.address().port}/v1/chat/completions`,{method:'POST',headers:{authorization:'Bearer '+key},body:JSON.stringify({model:'glm-5.3',messages:[{role:'user',content:'OK'}]})});assert.equal(r.status,429);assert.doesNotMatch(await r.text(),/provider-secret|sensitive/);}finally{s.closeAllConnections();await new Promise(r=>s.close(r));}
});
