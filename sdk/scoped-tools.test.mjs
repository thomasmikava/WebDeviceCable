import {test} from 'node:test';
import assert from 'node:assert/strict';
import {BridgeClient,BridgeError} from '../internal/bridge/web/index.js';

const schema={type:'object',properties:{value:{type:'string'}},required:['value'],additionalProperties:false};
const tool=(execute,id='search')=>({id,title:'Search',description:'Search pages',inputSchema:structuredClone(schema),execute});
const wait=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function until(check){for(let i=0;i<200;i++){if(check())return;await wait(10);}assert.fail('Expected callback state was not reached');}
function peer(){
  const requests=[],calls=[],completed=[],results=[],claims=[],jobs=new Map();let serial=0,session=0;
  const client=new BridgeClient({origin:'https://example.test',connection:{pairingId:'a'.repeat(64),identity:{deviceId:'device',endpoint:'',publicKey:''},privateKey:{},publicKey:''}},{reset(){},async close(){},async fetch(path,init={}){
    requests.push({path,...init});let body=init.body?JSON.parse(init.body):{};
    if(peerState.beforeRequest){const response=await peerState.beforeRequest(path,body);if(response)return response;}
    if(path==='/v2/capabilities')return Response.json({scopedToolsVersion:peerState.version,toolInputSchemaVersion:peerState.schemaVersion,toolOperationDetailsVersion:peerState.operationVersion,limits:peerState.limits});
    if(path==='/v2/lease')return Response.json({lease:'lease',expiresAt:new Date(Date.now()+60000).toISOString()});
    if(path==='/v2/conversations'){
      const conversation={id:'conversation-'+ ++session,status:'open',toolSetHash:'session-hash-'+session};
      if(peerState.beforeSessionResponse)await peerState.beforeSessionResponse(conversation);
      return Response.json(conversation);
    }
    if(path==='/v2/tool-calls/poll')return Response.json({calls,completed});
    if(path.endsWith('/claim')){claims.push(path);if(peerState.failClaim)throw new TypeError('Ambiguous claim');return Response.json({ok:true});}
    if(path.endsWith('/result')){results.push({path,...body});return Response.json({ok:true});}
    if(path==='/v2/admission')return Response.json({available:true});
    if(path==='/v2/jobs'||path.endsWith('/turns')){
      const conversationId=path.endsWith('/turns')?path.split('/')[3]:undefined;
      const job={id:'job-'+ ++serial,status:'running',request:{...body,tools:undefined,conversationId,toolSetHash:conversationId?'session-hash-'+session:'hash-'+serial}};
      jobs.set(job.id,job);
      if(peerState.beforeResponse){const response=await peerState.beforeResponse(job);if(response)return response;}
      return Response.json(job);
    }
    if(path.startsWith('/v2/jobs/'))return Response.json(jobs.get(path.split('/')[3]));
    return Response.json({status:'closed'});
  }});
  const peerState={client,requests,calls,completed,results,claims,jobs,beforeRequest:null,beforeResponse:null,beforeSessionResponse:null,version:1,schemaVersion:1,operationVersion:1,failClaim:false};
  return peerState;
}
const callback=(job,id='call',name='search')=>({id,name,arguments:{value:id},jobId:job.id,conversationId:job.request.conversationId,requestIdempotencyKey:job.request.idempotencyKey,toolSetHash:job.request.toolSetHash});

test('30-tool chat and 3-tool task isolate overlapping IDs, early callbacks and cancellation',async()=>{
  const p=peer(),seen=[],signals=[];await p.client.connect();
  try{
    const chatTools=Array.from({length:30},(_,i)=>tool((args,context)=>{seen.push(['chat',args.value,context]);signals.push(context.signal);return 'chat';},i?'chat_'+i:'search'));
    const ruleTools=Array.from({length:3},(_,i)=>tool((args,context)=>{seen.push(['rules',args.value,context]);signals.push(context.signal);return 'rules';},i?'rule_'+i:'search'));
    ruleTools[0].inputSchema.properties.value.description='Different rules schema';
    const chat=p.client.session({tools:chatTools});
    chatTools[0].description='Mutated outside snapshot';chatTools[0].execute=()=>assert.fail('Mutable handler leaked');
    p.beforeResponse=async job=>{p.calls.push(callback(job,job.id));await until(()=>p.results.some(result=>result.path.includes('/'+job.id+'/')));};
    const [chatJob,ruleJob]=await Promise.all([chat.send({prompt:'chat',idempotencyKey:'chat-key'}),p.client.submit({kind:'action',prompt:'rules',idempotencyKey:'rule-key',tools:ruleTools})]);
    assert.deepEqual(seen.map(value=>value[0]).sort(),['chat','rules']);
    assert.equal(p.requests.find(request=>request.path==='/v2/conversations').body.includes('Mutated outside'),false);
    assert.equal(seen.find(value=>value[0]==='chat')[2].conversationId,chat.id);
    assert.equal(seen.find(value=>value[0]==='rules')[2].conversationId,undefined);
    p.completed.push({jobId:ruleJob.id,requestIdempotencyKey:'rule-key',toolSetHash:ruleJob.request.toolSetHash});
    await until(()=>seen.find(value=>value[0]==='rules')[2].signal.aborted);
    assert.equal(seen.find(value=>value[0]==='chat')[2].signal.aborted,false);
    await chat.close();assert.equal(signals.every(signal=>signal.aborted),true);
    assert.equal(chatJob.request.toolSetHash.startsWith('session-hash'),true);
    assert.equal(p.claims.length,2);
  }finally{await p.client.close();}
  const old=peer();old.version=undefined;
  await assert.rejects(old.client.run({prompt:'unsupported',tools:[tool(()=>{})]}),error=>error.code==='desktop_update_required');
  assert.equal(old.requests.some(request=>request.path==='/v2/jobs'||request.path==='/v2/lease'),false);
});

test('preflight is read-only; invalid tools fail locally and unsupported desktops never submit',async()=>{
  const p=peer();await p.client.connect();
  try{
    await p.client.preflight({kind:'action',tools:[tool(()=>assert.fail('Preflight installed callbacks'))]});
    await wait(300);assert.equal(p.requests.some(request=>request.path==='/v2/tool-calls/poll'),false);
    assert.throws(()=>p.client.session({mode:'restricted',tools:[tool(()=>{})]}),error=>error.code==='invalid_mode');
    assert.throws(()=>p.client.session({id:'existing-context',tools:[tool(()=>{})]}),error=>error.code==='invalid_session');
    await assert.rejects(p.client.submit({kind:'action',idempotencyKey:'bad-schema',tools:[{...tool(()=>{}),inputSchema:{type:'object',properties:{},required:[],additionalProperties:true,$ref:'unsupported'}}]}),error=>error.category==='validation');
    const tooLarge=Array.from({length:600},(_,i)=>({...tool(()=>{},'tool_'+i),description:'x'.repeat(2000)}));
    await assert.rejects(p.client.submit({kind:'action',idempotencyKey:'too-large',tools:tooLarge}),error=>error.code==='tool_set_too_large');
    assert.throws(()=>p.client.session({tools:tooLarge}),error=>error.code==='tool_set_too_large');
    assert.equal(p.requests.some(request=>request.path==='/v2/jobs'),false);
    const session=p.client.session({tools:[tool(()=>{})]});await assert.rejects(session.send({prompt:'x',tools:[]}),error=>error.code==='invalid_tools');
  }finally{await p.client.close();}
});

test('300 tools work in preflight, standalone jobs and continuing sessions, including the last callback',async()=>{
  const p=peer(),seen=[];await p.client.connect();
  try {
    const tools=Array.from({length:300},(_,i)=>tool((_args,context)=>{seen.push(context);return i;},'tool_'+i));
    await p.client.preflight({kind:'action',tools});
    assert.equal(JSON.parse(p.requests.find(request=>request.path==='/v2/admission').body).request.tools.length,300);
    const chat=p.client.session({tools});
    const [job,turn]=await Promise.all([p.client.submit({kind:'action',idempotencyKey:'large-task',tools}),chat.send({prompt:'Continue',idempotencyKey:'large-chat'})]);
    assert.equal(JSON.parse(p.requests.find(request=>request.path==='/v2/jobs').body).tools.length,300);
    assert.equal(JSON.parse(p.requests.find(request=>request.path==='/v2/conversations').body).tools.length,300);
    p.calls.push(callback(job,'task-last','tool_299'),callback(turn,'chat-last','tool_299'));
    await until(()=>p.results.length===2);
    assert.equal(seen.length,2);assert.equal(seen.filter(context=>context.conversationId===chat.id).length,1);
    assert.ok(p.results.every(result=>result.result===299));
    await chat.close();
  } finally {await p.client.close();}
});

test('older desktop count caps produce an update requirement before submission or callback registration',async()=>{
  const p=peer();p.limits={toolsPerExecution:32};await p.client.connect();
  const tools=Array.from({length:33},(_,i)=>tool(()=>assert.fail('Unsupported tools ran'),'tool_'+i));
  try {
    for(const execute of [()=>p.client.preflight({kind:'action',tools}),()=>p.client.openSession({tools}),()=>p.client.submit({kind:'action',idempotencyKey:'legacy-count',tools})])await assert.rejects(execute(),error=>error.code==='desktop_update_required');
    assert.equal(p.client.executions.size,0);
    assert.equal(p.requests.some(request=>['/v2/admission','/v2/jobs','/v2/conversations','/v2/tool-calls/poll'].includes(request.path)),false);
  } finally {await p.client.close();}
});

test('callbacks are bounded to four, unmatched executions stay unclaimed, completion releases handlers',async()=>{
  const p=peer();await p.client.connect();let active=0,maximum=0;const release=[];
  try{
    const job=await p.client.submit({kind:'action',idempotencyKey:'bounded-key',tools:[tool(async()=>{maximum=Math.max(maximum,++active);await new Promise(resolve=>release.push(resolve));active--;return true;})]});
    p.calls.push({...callback(job,'unknown'),requestIdempotencyKey:'unknown-key'},...Array.from({length:6},(_,i)=>callback(job,'call-'+i)));
    await until(()=>active===4);assert.equal(maximum,4);assert.equal(p.claims.length,4);
    release.splice(0).forEach(resolve=>resolve());await until(()=>p.results.length===4);
    await until(()=>active===2);release.splice(0).forEach(resolve=>resolve());await until(()=>p.results.length===6);
    assert.equal(maximum,4);assert.equal(p.claims.some(path=>path.includes('unknown')),false);
    p.completed.push({jobId:job.id,requestIdempotencyKey:'bounded-key',toolSetHash:job.request.toolSetHash});
    await until(()=>p.client.executions.size===0);const polls=p.requests.filter(request=>request.path==='/v2/tool-calls/poll').length;
    await wait(550);assert.equal(p.requests.filter(request=>request.path==='/v2/tool-calls/poll').length,polls);
  }finally{release.forEach(resolve=>resolve());await p.client.close();}
});

test('catalog above 40 KiB is admitted while non-tool fields keep their budget',async()=>{
  const p=peer();await p.client.connect();
  try{
    const tools=Array.from({length:30},(_,i)=>({...tool(()=>{},'tool_'+i),description:'🙂\u0000\\"'.repeat(100),inputSchema:{...schema,description:'schema '.repeat(200)}}));
    await p.client.preflight({kind:'action',tools});
    assert.ok(Buffer.byteLength(p.requests.at(-1).body)>40960);
    await assert.rejects(p.client.preflight({kind:'action',tools,prompt:'x'.repeat(41000)}),error=>error.code==='request_too_large');
    await p.client.submit({kind:'action',idempotencyKey:'boundary',tools});
  }finally{await p.client.close();}
});

test('run captures tools before connecting, and ambiguous claims are never retried',async()=>{
  const p=peer();let executions=0;
  const definition=tool(()=>{executions++;return true;});
  p.beforeResponse=async job=>{
    p.calls.push(callback(job));await until(()=>p.results.length===1);
    throw new BridgeError('fixture_finished','Fixture stops before streaming.',400);
  };
  try{
    const running=p.client.run({prompt:'fixture',tools:[definition]});
    definition.description='Changed after invocation';definition.execute=()=>assert.fail('Changed handler escaped snapshot');
    await assert.rejects(running,error=>error.code==='fixture_finished');
    assert.equal(executions,1);
    assert.equal(JSON.parse(p.requests.find(request=>request.path==='/v2/jobs').body).tools[0].description,'Search pages');
    p.beforeResponse=null;p.calls.length=0;p.failClaim=true;
    const job=await p.client.submit({kind:'action',idempotencyKey:'uncertain-claim',tools:[tool(()=>{executions++;})]});
    p.calls.push(callback(job,'ambiguous-call'));
    await until(()=>p.claims.some(path=>path.includes('ambiguous-call')));
    await wait(600);
    assert.equal(p.claims.filter(path=>path.includes('ambiguous-call')).length,1);
    assert.equal(executions,1);
  }finally{await p.client.close();}
});

test('an unreadable submission response retains callbacks until its receipt resolves',async()=>{
  const p=peer();let executed=0;await p.client.connect();
  try{
    p.beforeResponse=()=>new Response('Unreadable acknowledged response',{status:200});
    await assert.rejects(p.client.submit({kind:'action',idempotencyKey:'unreadable-tool-submit',tools:[tool(()=>{executed++;return true;})]}),error=>error.code==='submission_unknown'&&error.idempotencyKey==='unreadable-tool-submit');
    const job=[...p.jobs.values()][0];p.calls.push(callback(job));
    await until(()=>p.results.length===1);assert.equal(executed,1);
    assert.equal(p.requests.filter(request=>request.path==='/v2/jobs').length,1);
    p.completed.push({jobId:job.id,requestIdempotencyKey:job.request.idempotencyKey,toolSetHash:job.request.toolSetHash});
    await until(()=>p.client.executions.size===0);
  }finally{await p.client.close();}
});

test('schema budgets account for canonical Unicode escaping without expanding HTML data',async()=>{
  const p=peer();await p.client.connect();
  try{
    const definition=tool(()=>true);
    definition.inputSchema.description='<>&'.repeat(2000);
    assert.equal((await p.client.preflight({kind:'action',tools:[definition]})).available,true);
    definition.inputSchema.description='\u2028'.repeat(2000);
    await assert.rejects(p.client.preflight({kind:'action',tools:[definition]}),error=>error.code==='invalid_tools'&&error.category==='validation');
  }finally{await p.client.close();}
});

test('a conflicting tool-free retry preserves the original execution callbacks',async()=>{
  const p=peer();let executed=0;await p.client.connect();
  try{
    const request={kind:'action',prompt:'fixture',idempotencyKey:'conflicting-tools',tools:[tool(()=>{executed++;return true;})]};
    const job=await p.client.submit(request),entry=p.client.executions.get(request.idempotencyKey);
    p.beforeRequest=path=>path==='/v2/jobs'?Response.json({error:{code:'idempotency_conflict',message:'Different tools'}},{status:409}):undefined;
    await assert.rejects(p.client.submit({...request,tools:[]}),error=>error.code==='idempotency_conflict');
    assert.equal(p.client.executions.get(request.idempotencyKey),entry);
    assert.equal(entry.controller.signal.aborted,false);
    p.calls.push(callback(job));await until(()=>p.results.length===1);assert.equal(executed,1);
  }finally{await p.client.close();}
});

test('session request identities include the conversation even with identical message bodies',async()=>{
  const p=peer();await p.client.connect();
  try{
    const first=await p.client.openSession({tools:[tool(()=>true)]});
    const request={prompt:'fixture',idempotencyKey:'conflicting-sessions'};
    const job=await p.client.sendSession(first.id,request),entry=p.client.executions.get(request.idempotencyKey);
    const second=await p.client.openSession({tools:[]});
    p.beforeRequest=path=>path.endsWith('/turns')?Response.json({error:{code:'idempotency_conflict',message:'Different conversation'}},{status:409}):undefined;
    await assert.rejects(p.client.sendSession(second.id,request),error=>error.code==='idempotency_conflict');
    assert.equal(p.client.executions.get(request.idempotencyKey),entry);
    assert.equal(entry.controller.signal.aborted,false);
    p.calls.push(callback(job));await until(()=>p.results.length===1);
  }finally{await p.client.close();}
});

test('a rejected matching retry does not own the original callback registration',async()=>{
  const p=peer();await p.client.connect();
  try{
    const request={kind:'action',prompt:'fixture',idempotencyKey:'matching-retry',tools:[tool(()=>true)]};
    const job=await p.client.submit(request),entry=p.client.executions.get(request.idempotencyKey);
    p.beforeRequest=path=>path==='/v2/jobs'?Response.json({error:{code:'rate_limited',message:'Temporary rejection'}},{status:429}):undefined;
    await assert.rejects(p.client.submit(request),error=>error.status===429);
    assert.equal(p.client.executions.get(request.idempotencyKey),entry);
    assert.equal(entry.controller.signal.aborted,false);
    p.calls.push(callback(job));await until(()=>p.results.length===1);
  }finally{await p.client.close();}
});

test('concurrent matching submissions keep callbacks when either attempt is admitted',async()=>{
  const p=peer();let rejectFirst;await p.client.connect();
  try{
    const request={kind:'action',prompt:'fixture',idempotencyKey:'concurrent-matching',tools:[tool(()=>true)]};
    p.beforeRequest=path=>path==='/v2/jobs'?new Promise(resolve=>{rejectFirst=()=>resolve(Response.json({error:{code:'not_admitted',message:'First attempt was not admitted'}},{status:400}));}):undefined;
    const first=assert.rejects(p.client.submit(request),error=>error.code==='not_admitted');
    await until(()=>Boolean(rejectFirst));p.beforeRequest=null;
    const job=await p.client.submit(request),entry=p.client.executions.get(request.idempotencyKey);
    rejectFirst();await first;
    assert.equal(p.client.executions.get(request.idempotencyKey),entry);
    assert.equal(entry.controller.signal.aborted,false);
    p.calls.push(callback(job));await until(()=>p.results.length===1);
  }finally{rejectFirst?.();await p.client.close();}
});

test('all definitively rejected matching attempts release their provisional callbacks',async()=>{
  const p=peer();let rejectFirst;await p.client.connect();
  try{
    const request={kind:'action',prompt:'fixture',idempotencyKey:'rejected-matching',tools:[tool(()=>assert.fail('Rejected submission callback'))]};
    const rejected=()=>Response.json({error:{code:'not_admitted',message:'Request was not admitted'}},{status:400});
    p.beforeRequest=path=>path==='/v2/jobs'?new Promise(resolve=>{rejectFirst=()=>resolve(rejected());}):undefined;
    const first=assert.rejects(p.client.submit(request),error=>error.code==='not_admitted');
    await until(()=>Boolean(rejectFirst));const entry=p.client.executions.get(request.idempotencyKey);
    p.beforeRequest=path=>path==='/v2/jobs'?rejected():undefined;
    await assert.rejects(p.client.submit(request),error=>error.code==='not_admitted');
    assert.equal(p.client.executions.get(request.idempotencyKey),entry);
    rejectFirst();await first;
    assert.equal(p.client.executions.has(request.idempotencyKey),false);
    assert.equal(entry.controller.signal.aborted,true);
    assert.equal(p.client.toolsTimer,undefined);
  }finally{rejectFirst?.();await p.client.close();}
});

test('a delayed rejection cannot release a new registration after reconnecting',async()=>{
  const p=peer();let rejectOld;await p.client.connect();
  try{
    const request={kind:'action',prompt:'fixture',idempotencyKey:'delayed-rejection',tools:[tool(()=>true)]};
    p.beforeRequest=path=>path==='/v2/jobs'?new Promise(resolve=>{rejectOld=()=>resolve(Response.json({error:{code:'not_admitted',message:'Request was not admitted'}},{status:400}));}):undefined;
    const oldSubmission=assert.rejects(p.client.submit(request),error=>error.code==='not_admitted');
    await until(()=>Boolean(rejectOld));await p.client.close();await p.client.connect();p.beforeRequest=null;
    const job=await p.client.submit(request),entry=p.client.executions.get(request.idempotencyKey);
    rejectOld();await oldSubmission;
    assert.equal(p.client.executions.get(request.idempotencyKey),entry);
    assert.equal(entry.controller.signal.aborted,false);
    p.calls.push(callback(job));await until(()=>p.results.length===1);
  }finally{rejectOld?.();await p.client.close();}
});

test('session creation responses cannot restore snapshots after closure or reconnecting',async()=>{
  for(const reconnect of [false,true]){
    const p=peer();let releaseOld,oldId;await p.client.connect();
    try{
      p.beforeSessionResponse=conversation=>new Promise(resolve=>{oldId=conversation.id;releaseOld=resolve;});
      const chat=p.client.session({tools:[tool(()=>assert.fail('Closed session callback'))]});
      const opening=assert.rejects(chat.send('fixture'),error=>error.code==='lease_closed');
      await until(()=>Boolean(releaseOld));await p.client.close();
      let current;
      if(reconnect){await p.client.connect();p.beforeSessionResponse=null;current=await p.client.openSession({tools:[tool(()=>true)]});}
      releaseOld();await opening;
      assert.equal(p.client.sessions.has(oldId),false);
      assert.equal(p.client.sessions.size,reconnect?1:0);
      if(current)assert.equal(p.client.sessions.has(current.id),true);
      assert.equal(p.requests.some(request=>request.path.endsWith('/turns')),false);
    }finally{releaseOld?.();await p.client.close();}
  }
});


test('natural tool inputs preflight and operation reporting bind to the claimed callback',async()=>{
 const p=peer();await p.client.connect();let context;
 try {
  const definition={...tool(async(args,ctx)=>{context=ctx;assert.deepEqual(args,{value:'call'});await ctx.reportOperation({id:'rename_page',title:'Renaming a page',version:'v1'});return true;}),operationDetails:true,inputSchema:{type:'object',properties:{value:{type:['string','null']},arguments:{type:'object',additionalProperties:true}},required:['value'],additionalProperties:false}};
  await p.client.preflight({kind:'action',tools:[definition]});assert.equal(p.client.executions.size,0);
  const job=await p.client.submit({kind:'action',idempotencyKey:'natural-inputs',tools:[definition]});p.calls.push(callback(job));await until(()=>p.results.length===1);
  const report=p.requests.find(request=>request.path.endsWith('/operation'));assert.deepEqual(JSON.parse(report.body),{id:'rename_page',title:'Renaming a page',version:'v1'});
  assert.ok(p.requests.findIndex(request=>request.path.endsWith('/claim'))<p.requests.indexOf(report));
  await assert.rejects(context.reportOperation({id:'x',title:'Bad\nlabel'}),error=>error.code==='invalid_operation');
  p.completed.push({jobId:job.id,requestIdempotencyKey:'natural-inputs',toolSetHash:job.request.toolSetHash});await until(()=>context.signal.aborted);
  await assert.rejects(context.reportOperation({id:'x',title:'Ended'}),error=>error.code==='execution_ended');
 } finally {await p.client.close();}
});

test('new tool features fail before submission against older desktops',async()=>{
 for(const feature of ['schemaVersion','operationVersion']) {
  const p=peer();p[feature]=undefined;await p.client.connect();
  try {const definition=feature==='schemaVersion'?{...tool(()=>{}),inputSchema:{type:'object',additionalProperties:true}}:{...tool(()=>{}),operationDetails:true};
   for(const action of [()=>p.client.preflight({kind:'action',tools:[definition]}),()=>p.client.openSession({tools:[definition]}),()=>p.client.submit({kind:'action',idempotencyKey:'unsupported-features',tools:[definition]})])await assert.rejects(action(),error=>error.code==='desktop_update_required');
   assert.equal(p.client.executions.size,0);assert.equal(p.requests.some(request=>['/v2/admission','/v2/jobs','/v2/conversations'].includes(request.path)),false);
  } finally {await p.client.close();}
 }
});


test('a verified file-result rejection returns its uncertainty without repeating the claimed handler',async()=>{
 const p=peer();let executed=0;
 p.beforeRequest=(path,body)=>path.endsWith('/result')&&body.result?.$wdc==='tool-result'?Response.json({error:{code:'invalid_input',message:'This upload belongs to another lease.'}},{status:400}):undefined;
 try {
  await p.client.connect();const job=await p.client.submit({kind:'action',idempotencyKey:'foreign-file-result',tools:[tool(()=>{executed++;return {$wdc:'tool-result',result:{saved:true},inputs:['a'.repeat(64)]};})]});
  p.calls.push(callback(job,'file-callback'));
  await until(()=>p.results.length===1);
  assert.equal(executed,1);assert.equal(p.claims.length,1);
  assert.deepEqual(p.results[0].result.result,{saved:true});assert.equal(p.results[0].result.fileDelivery.code,'invalid_input');assert.match(p.results[0].result.fileDelivery.message,/do not.*repeat/i);
 } finally {await p.client.close();}
});
