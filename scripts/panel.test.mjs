// UI lifecycle checks without a browser, network, or provider execution.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
const source=(await readFile(new URL('../internal/bridge/web/app.js',import.meta.url),'utf8')).replace(/^import .*;\n/,'').replace(/^void boot\(\);$/m,'');
class Element {
  constructor(tag='div'){this.tagName=tag.toUpperCase();this.children=[];this.dataset={};this.attrs={};this.value='';this.hidden=false;this.classList={toggle(){}};this.elements=new Proxy({namedItem:()=>new Element()}, {get:(target,key)=>target[key]??new Element()});}
  setAttribute(key,value){this.attrs[key]=String(value);if(key==='value')this.value=String(value);}
  getAttribute(key){return this.attrs[key];}
  append(...nodes){this.children.push(...nodes);for(const node of nodes)if(node instanceof Element)node.parentElement=this;}
  prepend(node){this.children.unshift(node);}
  replaceChildren(...nodes){this.children=[];this.append(...nodes);}
  get options(){return this.children;}
  addEventListener(){}
  querySelector(selector){if(selector==='.form-error, .form-notice')return this.children.find(node=>node instanceof Element&&['form-error','form-notice'].includes(node.className))??null;return selector==='.dialog-actions'?this.footer:null;}
  querySelectorAll(){return [];}
  before(node){this.parentElement.append(node);}
  remove(){if(this.parentElement)this.parentElement.children=this.parentElement.children.filter(node=>node!==this);}
  setCustomValidity(value){this.validationMessage=value;}
  focus(){}
}
const storage=()=>{const data=new Map();return {getItem:key=>data.get(key)??null,setItem:(key,value)=>data.set(key,String(value)),removeItem:key=>data.delete(key)};};
function panel(options={}){
  const nodes=new Map(),alerts=[],get=id=>{if(!nodes.has(id))nodes.set(id,new Element());return nodes.get(id);};
  const document={hidden:false,title:'',hasFocus:()=>true,body:new Element(),getElementById:get,querySelector:selector=>selector==='.skip-link'?get('skip'):null,querySelectorAll:()=>[],createElement:tag=>new Element(tag),createElementNS:(_,tag)=>new Element(tag),createTextNode:text=>String(text),addEventListener(){}};
  class Notification {static permission='granted';constructor(title,options){this.title=title;this.options=options;alerts.push(this);}close(){this.closed=true;}}
  const window={Notification,localStorage:options.local??storage(),sessionStorage:storage(),addEventListener(){},focus(){this.focused=true;},scrollTo(){}};
  const context=vm.createContext({document,window,Notification,Node:Element,HTMLElement:Element,HTMLButtonElement:Element,HTMLSelectElement:Element,HTMLDialogElement:Element,localStorage:window.localStorage,sessionStorage:window.sessionStorage,URL,URLSearchParams,AbortSignal,crypto:globalThis.crypto,location:{hash:''},setInterval(){},setTimeout(){},clearInterval(){},clearTimeout(){},requestAnimationFrame:fn=>fn(),console});
  vm.runInContext(source,context);
  const run=code=>vm.runInContext(code,context);
  run(`state={deviceId:'fixture',pairings:[],jobs:[],permissions:[],providers:[],programs:[],pairingRequests:[],relayConnections:[]};csrf='test';loadDevicePreferences();`);
  return {context,run,nodes,get,alerts,document,window};
}
test('a pending selection waits for its exact server-issued pairing ID and is saved once',async()=>{
  const p=panel(),writes=[];p.context.apiImpl=async(path,init)=>{writes.push({path,body:JSON.parse(init.body)});return {revision:1,default:{provider:'codex'}};};p.run('api=apiImpl;rememberDefault({requestId:"request",expiresAt:Date.now()+60000},"codex");state.pairings=[{id:"unrelated",origin:"https://same.example"}];');
  await p.run('applyPendingDefaults()');assert.equal(writes.length,0);
  p.run('state.pairingRequests=[{id:"request",pairingId:"new-pair"}];');await p.run('applyPendingDefaults()');assert.equal(writes.length,0);
  p.run('state.pairings.push({id:"new-pair",routing:{revision:0}});');await p.run('applyPendingDefaults()');await p.run('applyPendingDefaults()');
  assert.equal(writes.length,1);assert.equal(writes[0].path,'/pairings/new-pair/routing');assert.deepEqual(writes[0].body,{expectedRevision:0,routing:{default:{provider:'codex'},groups:{}}});
});
test('a saved route from another panel is never overwritten',async()=>{
  const p=panel();p.context.apiImpl=()=>{throw Error('unexpected write');};p.run('api=apiImpl;rememberDefault({pairingId:"pair"},"codex");state.pairings=[{id:"pair",routing:{revision:2,default:{provider:"claude"}}}];');await p.run('applyPendingDefaults()');assert.equal(p.run('pendingDefaults.length'),0);
});
test('manual invitation default survives reloading before its pairing appears',async()=>{
  const p=panel();p.run('rememberDefault({pairingId:"pair"},"codex");pendingDefaults=[];localDevice="";');p.context.apiImpl=async()=>({revision:1,default:{provider:'codex'}});p.run('api=apiImpl;state.pairings=[{id:"pair"}];');await p.run('applyPendingDefaults()');assert.equal(p.run('state.pairings[0].routing.default.provider'),'codex');
});
test('default picker chooses the first ready native program without granting custom access',()=>{
  const p=panel();p.run('state.providers=[{id:"custom",execution:true,name:"Custom"},{id:"claude",execution:false,name:"Claude"},{id:"codex",execution:true,name:"Codex"}];state.programs=[{id:"custom",type:"custom"},{id:"claude",type:"claude"},{id:"codex",type:"codex"}];fillDefaultPrograms($("select"));');assert.equal(p.get('select').value,'codex');assert.equal(p.get('select').options.find(option=>option.value==='custom').disabled,true);
});
test('title counts all three types waiting on the user and excludes an approved handoff',()=>{
  const p=panel();p.run('state.pairingRequests=[{id:"connect",name:"App",status:"pending"},{id:"approved",status:"approved"}];state.jobs=[{id:"job",status:"awaiting_approval",request:{kind:"text"}}];state.permissions=[{id:"access",kind:"files"}];renderAttention();');assert.equal(p.document.title,'(3) WebDeviceCable');assert.equal(p.get('notification-hint').hidden,false);
});
test('background notifications fire once per request and use app names',()=>{
  const p=panel();p.document.hidden=true;p.run('notificationsEnabled=true;state.pairings=[{id:"pair",name:"Writer"}];state.pairingRequests=[{id:"connect",name:"New app",status:"pending"}];state.jobs=[{id:"job",pairingId:"pair",status:"awaiting_approval",request:{kind:"connection-test"}}];state.permissions=[{id:"access",pairingId:"pair",kind:"files"}];renderAttention();renderAttention();');assert.equal(p.alerts.length,3);assert.deepEqual(p.alerts.map(alert=>alert.title),['New app','Writer','Writer']);assert.match(p.alerts[1].options.body,/connection test/);
  p.alerts[1].onclick();assert.equal(p.context.location.hash,'activity');assert.equal(p.window.focused,true);
  p.run('state.jobs=[];renderAttention();');assert.equal(p.alerts[1].closed,true);
});
test('foreground requests and disabled or denied notifications do not create desktop alerts',()=>{
  const p=panel();p.run('notificationsEnabled=true;state.jobs=[{id:"job",status:"awaiting_approval",request:{kind:"text"}}];renderAttention();');assert.equal(p.alerts.length,0);
  p.document.hidden=true;p.run('renderAttention();');assert.equal(p.alerts.length,0);
  p.run('Notification.permission="denied";renderAttention();');assert.match(p.get('notification-help').textContent,/blocked/);assert.equal(p.get('notification-hint').hidden,false);
});
test('Always allow cancellation changes neither permission nor request',async()=>{
  const p=panel();p.context.apiImpl=()=>{throw Error('unexpected write');};p.run('api=apiImpl;confirmAction=async()=>false;');await p.run('alwaysAllowRequest({id:"pair",name:"Writer"},{id:"job"})');
});
test('Always allow saves only automatic permission with a revision before approving',async()=>{
  const p=panel(),writes=[];p.context.apiImpl=async(path,init)=>{writes.push({path,body:JSON.parse(init.body)});return {};};p.run('api=apiImpl;confirmAction=async()=>true;');await p.run('alwaysAllowRequest({id:"pair",name:"Writer",permissionsRevision:3},{id:"job"})');assert.equal(writes[0].path,'/pairings/pair');assert.equal(writes[0].body.automatic,true);assert.equal(writes[0].body.expectedPermissionsRevision,3);assert.equal(writes[0].body.files,undefined);assert.equal(writes[1].path,'/jobs/job/decision');assert.deepEqual(writes[1].body,{approve:true});
});
test('success and info feedback stays visible inside the active modal',()=>{
  const p=panel(),dialog=new Element();dialog.footer=new Element();dialog.append(dialog.footer);p.document.querySelectorAll=selector=>selector==='dialog[open]'?[dialog]:[];
  p.run('notify("Copied.","success")');let notice=dialog.children.find(node=>node.className==='form-notice');assert.equal(notice.textContent,'Copied.');assert.equal(notice.attrs.role,'status');
  p.run('notify("Select a program.","info")');assert.equal(notice.textContent,'Select a program.');assert.equal(dialog.children.filter(node=>node.className==='form-notice').length,1);
  p.run('notify("Save failed.")');assert.equal(notice.className,'form-error');assert.equal(notice.attrs.role,'alert');
});
test('a readiness update chooses a program until the user explicitly chooses otherwise',()=>{
  const p=panel();p.run('const info={name:"New app"};reviewDraft(info,"request");state.providers=[{id:"codex",execution:true,name:"Codex"}];state.programs=[{id:"codex",type:"codex"}];connectionReview(info,"request");');assert.equal(p.run('reviewDrafts.get("request").provider'),'codex');p.run('Object.assign(reviewDrafts.get("request"),{provider:"",providerChosen:true});connectionReview(info,"request");');assert.equal(p.run('reviewDrafts.get("request").provider'),'');
});
test('a failed delayed route save remains retryable without replaying approval',async()=>{
  const p=panel();let writes=0;p.context.apiImpl=async()=>{writes++;if(writes===1)throw Object.assign(Error('Offline'),{status:503});return {revision:1,default:{provider:'codex'}};};p.run('api=apiImpl;rememberDefault({pairingId:"pair"},"codex");state.pairings=[{id:"pair"}];');await p.run('applyPendingDefaults()');assert.equal(p.run('pendingDefaults.length'),1);await p.run('applyPendingDefaults()');assert.equal(writes,1);p.run('pendingDefaults[0].retryAfter=0;');await p.run('applyPendingDefaults()');assert.equal(writes,2);assert.equal(p.run('pendingDefaults.length'),0);
});
test('notifications already seen in this tab survive a panel reload',()=>{
  const p=panel();p.document.hidden=true;p.run('notificationsEnabled=true;state.jobs=[{id:"job",status:"awaiting_approval",request:{kind:"connection-test"}}];renderAttention();notifiedRequests.clear();localDevice="";loadDevicePreferences();renderAttention();');assert.equal(p.alerts.length,1);
});
test('returning from Access preserves the unsaved program draft and uses current grants',async()=>{
  const p=panel();p.context.showRoute=pair=>{p.context.shown=pair;};p.run('refresh=async()=>{};openRouting=showRoute;state.pairings=[{id:"pair",name:"Writer",actions:{command:"new-hash"},routing:{revision:2,default:{provider:"claude"}}}];routingReturn={pairID:"pair",revision:2,draft:{default:{provider:"codex"},groups:{}}};');await p.run('restoreRoutingAfterAccess()');assert.equal(p.context.shown.routing.default.provider,'codex');assert.equal(p.context.shown.actions.command,'new-hash');assert.equal(p.run('routingReturn'),undefined);
});
test('a changed routing revision replaces the draft after Access rather than overwriting it',async()=>{
  const p=panel();p.context.showRoute=pair=>{p.context.shown=pair;};p.run('refresh=async()=>{};openRouting=showRoute;state.pairings=[{id:"pair",routing:{revision:3,default:{provider:"claude"}}}];routingReturn={pairID:"pair",revision:2,draft:{default:{provider:"codex"},groups:{}}};');await p.run('restoreRoutingAfterAccess()');assert.equal(p.context.shown.routing.default.provider,'claude');assert.match(p.get('notice-text').textContent,/Programs and models changed/);
});
test('locking clears any routing draft waiting behind an Access editor',()=>{
  const p=panel();p.run('routingReturn={pairID:"pair",revision:1,draft:{default:{provider:"codex"}}};lock();');assert.equal(p.run('routingReturn'),undefined);assert.equal(p.get('locked').hidden,false);
});

test('pending invitation metadata survives reload and appears in a second tab without its token',()=>{
  const p=panel(),q=panel();
  const metadata=JSON.stringify([{id:'pair',manual:true,pairing:{id:'pair',name:'Writer',origin:'https://writer.example',appUser:'alice'},expiresAt:new Date(Date.now()+60000).toISOString()}]);
  p.run('invitationCopies=[{pairingId:"pair",invitation:"private-token"}];saveInvitations();invitationCopies=[];localDevice="";loadDevicePreferences();');
  p.run(`syncInvitations(${metadata});`);q.run(`syncInvitations(${metadata});`);
  assert.equal(p.run('pendingInvitations[0].invitation'),'private-token');assert.equal(q.run('pendingInvitations[0].name'),'Writer');assert.equal(q.run('pendingInvitations[0].invitation'),undefined);
  q.run('preserveList=(root,nodes)=>root.replaceChildren(...nodes);renderInvitations();');
  const text=node=>typeof node==='string'?node:(node.children??[]).map(text).join(' ');
  const content=text(q.get('invitation-list'));assert.match(content,/Cancel/);assert.doesNotMatch(content,/Copy again/);assert.match(content,/only in the tab/);
});
test('confirmed cancellation revokes on the server before removing metadata, token and pending default',async()=>{
  const p=panel(),writes=[];p.context.apiImpl=async(path,init)=>{writes.push({path,method:init.method});assert.equal(p.run('pendingInvitations.length'),1);return {ok:true};};
  p.run('api=apiImpl;confirmAction=async()=>true;preserveList=(root,nodes)=>root.replaceChildren(...nodes);const entry={pairingId:"pair",name:"Writer",invitation:"secret",expiresAt:Date.now()+60000};pendingInvitations=[entry];invitationCopies=[entry];rememberDefault({pairingId:"pair"},"codex");saveInvitations();');
  await p.run('cancelInvitation(entry)');assert.deepEqual(writes,[{path:'/invitations/pair',method:'DELETE'}]);
  assert.equal(p.run('pendingInvitations.length+invitationCopies.length+pendingDefaults.length'),0);assert.equal(p.window.sessionStorage.getItem('wdc-invitations:fixture'),'[]');
});
test('cancel dismissal and failed revocation preserve pending invitations',async()=>{
  const p=panel();let writes=0;p.context.apiImpl=async()=>{writes++;throw new Error('save failed');};
  p.run('api=apiImpl;confirmAction=async()=>false;const entry={pairingId:"pair",name:"Writer"};pendingInvitations=[entry];invitationCopies=[entry];');await p.run('cancelInvitation(entry)');assert.equal(writes,0);
  p.run('confirmAction=async()=>true;');await assert.rejects(p.run('cancelInvitation(entry)'),/save failed/);assert.equal(writes,1);assert.equal(p.run('pendingInvitations.length'),1);assert.equal(p.run('invitationCopies.length'),1);
});
test('another tab revoking an invitation clears its creating tab copy and disables the open copy dialog',()=>{
  const p=panel();p.run('const entry={pairingId:"pair",invitation:"secret",expiresAt:Date.now()+60000};invitationCopies=[entry];pendingInvitations=[entry];currentInvitation=entry;invitation="secret";syncInvitations([]);updateCountdowns();');
  assert.equal(p.run('invitationCopies.length'),0);assert.equal(p.run('invitation'),undefined);assert.equal(p.get('copy-invite').disabled,true);assert.equal(p.get('invite-countdown').textContent,'Invitation cancelled.');
});
test('used and expired invitations disable copying and do not remain pending',()=>{
  for(const used of [false,true]){
    const p=panel();p.run(`const entry={pairingId:"pair",invitation:"secret",expiresAt:Date.now()+${used?60000:-1}};pendingInvitations=[entry];invitationCopies=[entry];currentInvitation=entry;state.pairings=${used?'[{id:"pair"}]':'[]'};preserveList=(root,nodes)=>root.replaceChildren(...nodes);renderInvitations();`);
    assert.equal(p.run('pendingInvitations.length+invitationCopies.length'),0);assert.equal(p.get('copy-invite').disabled,true);assert.equal(p.get('invite-countdown').textContent,used?'Invitation used.':'Expired');
  }
});
test('an app requesting automatic approvals starts off and needs the owner choice and confirmation',async()=>{
  const p=panel();p.run('const info={name:"Writer",requestedPermissions:{automaticApprovals:true}};const review=connectionReview(info,"request");const automatic=review.children[1].children[0];confirmAutomatic=async()=>false;');
  assert.equal(p.run('reviewDecision("request").automatic'),false);
  p.run('automatic.checked=true;');await p.run('automatic.onchange()');assert.equal(p.run('reviewDecision("request").automatic'),false);
  p.run('confirmAutomatic=async()=>true;automatic.checked=true;');await p.run('automatic.onchange()');assert.equal(p.run('reviewDecision("request").automatic'),true);
});

test('history choices resolve both overrides and show the current global value',()=>{
  const p=panel();p.run('const choices=requestHistoryChoice({});');
  assert.deepEqual(Array.from(p.run('choices.options')).map(option=>option.value),['global','keep','for','discard']);
  assert.match(p.run('choices.options[0].children.join("")'),/Same as global \(Keep until I clear it\)/);
  p.run('state.keepRequestHistory=false;');assert.match(p.run('globalHistoryLabel()'),/Don’t keep/);
  for(const [global,choice,want] of [[false,'keep',true],[true,'discard',false],[false,'global',false],[true,'global',true]]){
    assert.equal(p.run(`state.keepRequestHistory=${global};effectiveRequestHistory({requestHistory:${JSON.stringify(choice)}})`),want);
  }
  p.run('state.keepRequestHistory=false;');
  assert.equal(p.run('historyNeedsConfirmation({requestHistory:"keep"},"global")'),true);
  assert.equal(p.run('historyNeedsConfirmation({requestHistory:"global"},"discard")'),false);
  assert.equal(p.run('historyNeedsConfirmation({requestHistory:"discard"},"keep")'),false);
});
test('Requests explains global and per-connection opt-outs and ignores revoked connections',()=>{
  const p=panel();p.run('renderHistorySettings();');assert.equal(p.get('history-note').hidden,true);
  p.run('state.pairings=[{requestHistory:"discard"}];renderHistorySettings();');assert.equal(p.get('history-note').hidden,false);assert.match(p.get('history-note').textContent,/some connections/);
  p.run('state.pairings[0].revoked=true;renderHistorySettings();');assert.equal(p.get('history-note').hidden,true);
  p.run('state.keepRequestHistory=false;state.pairings=[{requestHistory:"keep"}];renderHistorySettings();');assert.match(p.get('history-note').textContent,/off globally/);
});

function historyAccessPanel(){
  const p=panel(),dialog=new Element('dialog'),heading=new Element(),title=new Element('h2'),close=new Element('button');
  dialog.showModal=()=>{dialog.open=true;};dialog.close=()=>{dialog.open=false;};dialog.removeAttribute=key=>delete dialog.attrs[key];
  heading.querySelector=()=>title;dialog.querySelector=selector=>selector==='.dialog-heading'?heading:selector==='[aria-label="Close"]'?close:null;
  p.window.removeEventListener=()=>{};p.context.dialogImpl=dialog;p.context.makeDialogImpl=()=>dialog;
  p.run('makeDialog=makeDialogImpl;refresh=async()=>{};state.commands=[];state.programs=[];openAppPermissions({id:"pair",name:"Writer",permissionsRevision:3,requestHistory:"global"});');
  const descendants=node=>[node,...(node.children??[]).filter(child=>child instanceof Element).flatMap(descendants)];
  const all=descendants(dialog),select=all.find(node=>node.tagName==='SELECT'),save=all.find(node=>node.attrs['data-action']==='Save changes');
  return {...p,dialog,select,save};
}
test('Access stages history in its existing draft and cancellation of purge confirmation preserves it',async()=>{
  const p=historyAccessPanel();let writes=0;
  p.context.apiImpl=async()=>{writes++;};p.run('api=apiImpl;confirmAction=async()=>false;');
  assert.equal(p.save.disabled,true);p.select.value='discard';p.select.onchange();assert.equal(p.save.disabled,false);assert.equal(writes,0);
  await p.save.onclick();assert.equal(writes,0);assert.equal(p.select.value,'discard');assert.equal(p.dialog.open,true);
});
test('Access saves a confirmed history override with its revision and immutable save receipt',async()=>{
  const p=historyAccessPanel(),writes=[];
  p.context.apiImpl=async(path,init)=>{const body=JSON.parse(init.body);writes.push({path,body});return {permissionSaveId:body.permissionSaveId,requestHistory:'discard'};};
  p.run('api=apiImpl;confirmAction=async()=>true;');p.select.value='discard';p.select.onchange();await p.save.onclick();
  assert.equal(writes[0].path,'/pairings/pair');assert.equal(writes[0].body.requestHistory,'discard');assert.equal(writes[0].body.confirmDiscard,true);assert.equal(writes[0].body.expectedPermissionsRevision,3);assert.match(writes[0].body.permissionSaveId,/^[a-f0-9]{64}$/);assert.equal(writes[0].body.automatic,undefined);
});

test('shared quantity control restricts units, validates bounds and stores canonical values',()=>{
  const p=panel();p.run('const q=quantityControl("Maximum request time",600,{seconds:1,minutes:60,hours:3600},1,3600);');
  assert.deepEqual(Array.from(p.run('q.unit.options')).map(o=>o.value),['seconds','minutes','hours']);
  assert.equal(p.run('q.read()'),600);p.run('q.unit.value="hours";q.unit.onchange();');assert.equal(p.run('q.read()'),600);assert.equal(p.run('q.valid()'),true);
  for(const unit of ['seconds','minutes','hours']){p.run(`q.unit.value=${JSON.stringify(unit)};q.unit.onchange();`);assert.equal(p.run('q.read()'),600);assert.equal(p.run('q.valid()'),true);}
  p.run('q.number.value="2";q.number.oninput();');assert.equal(p.run('q.valid()'),false);assert.equal(p.run('q.number.attrs["aria-invalid"]'),'true');
  p.run('q.number.value="0";q.number.oninput();');assert.equal(p.run('q.valid()'),false);
  p.run('const size=quantityControl("Output size limit",1048576,sizeUnits,4096,2097152);');assert.deepEqual(Array.from(p.run('size.unit.options')).map(o=>o.value),['KB','MB']);assert.equal(p.run('size.read()'),1048576);
  p.run('size.unit.value="KB";size.unit.onchange();');assert.equal(p.run('size.number.value'),'1024');assert.equal(p.run('size.read()'),1048576);
  p.run('size.unit.value="MB";size.unit.onchange();size.number.value="0.5";size.number.oninput();');assert.equal(p.run('size.read()'),524288);assert.equal(p.run('size.valid()'),true);
  p.run('size.number.value="3";size.number.oninput();');assert.equal(p.run('size.valid()'),false);
});
test('all connection limits default to global, support never and retain canonical overrides',()=>{
 const p=panel();p.run('state.settings={maximumRequestSeconds:3600,outputBytes:8192,connectionExpirySeconds:86400};const max=overrideControl("Maximum request time","maximumRequestSeconds",{},3600,{seconds:1,minutes:60,hours:3600},1,3600,()=>{});const expiry=overrideControl("Connection expiry","connectionExpirySeconds",{overrides:{connectionExpirySeconds:0}},86400,{hours:3600,days:86400},3600,31536000,()=>{});');
 assert.equal(p.run('max.read()'),undefined);assert.match(p.run('max.select.options[0].children.join("")'),/Same as global \(1 hour\)/);assert.equal(p.run('expiry.read()'),0);
 p.run('max.select.value="custom";max.q.number.value="30";max.q.unit.value="minutes";max.select.onchange();');assert.equal(p.run('max.read()'),1800);assert.equal(p.run('max.valid()'),true);
 assert.equal(p.run('hasConnectionOverrides({overrides:{connectionExpirySeconds:0}})'),true);assert.equal(p.run('hasConnectionOverrides({overrides:{}})'),false);
});
test('global routing preselects inheritance and resolves workflow then connection then global',()=>{
 const p=panel();p.run('state.settings={default:{provider:"codex",model:"global",reasoning:"high"}};state.providers=[{id:"codex",name:"Codex"}];state.programs=[{id:"codex",type:"codex"}];fillDefaultPrograms($("global-picker"));');
 assert.equal(p.get('global-picker').value,'');assert.match(p.get('global-picker').options[0].children.join(''),/Same as global \(Codex\)/);
 p.run('const pair={routing:{default:{inheritOptions:true,model:"connection"},groups:{ai:{inheritOptions:true,reasoning:"low"}}},groups:[{id:"ai"}]};');assert.equal(p.run('connectionRoute(pair,"ai").model'),'connection');assert.equal(p.run('connectionRoute(pair,"ai").reasoning'),'low');assert.equal(p.run('connectionRoute({}).provider'),'codex');
 assert.equal(p.run('reviewDraft({name:"App"},"global-default").provider'),'');
});
test('timed history uses seconds through days, confirms reductions, and preserves explicit keep',()=>{
 const p=panel();p.run('state.settings={history:{mode:"for",seconds:86400}};const h=historyControl({requestHistory:"for",historySeconds:3600});');
 assert.deepEqual(Array.from(p.run('h.quantity.unit.options')).map(o=>o.value),['seconds','minutes','hours','days']);assert.equal(p.run('h.read().seconds'),3600);
 assert.equal(p.run('historyNeedsConfirmation({requestHistory:"global"},"for",3600)'),true);assert.equal(p.run('historyNeedsConfirmation({requestHistory:"for",historySeconds:3600},"global")'),false);assert.equal(p.run('connectionHistoryPolicy({requestHistory:"keep"}).mode'),'keep');
 p.run('h.select.value="discard";h.select.onchange();');assert.equal(p.run('h.read().mode'),'discard');assert.equal(p.run('h.quantity.node.hidden'),true);
});
test('notification event choices, connection mute and failed notices use persistent browser preferences',()=>{
 const p=panel();p.document.hidden=true;p.run('notificationsEnabled=true;notificationOptions.job=false;state.pairings=[{id:"muted",overrides:{muteNotifications:true}},{id:"live",name:"Writer"}];state.jobs=[{id:"approval",pairingId:"live",status:"awaiting_approval",request:{kind:"text"}},{id:"failure",pairingId:"muted",status:"failed"}];state.failedRequests=[{id:"hidden-failure",pairingId:"live"}];renderAttention();');
 assert.equal(p.alerts.length,1);assert.equal(p.alerts[0].title,'Writer');assert.equal(p.alerts[0].options.silent,true);
 p.run('renderNotificationOptions();');const all=node=>[node,...(node.children??[]).filter(n=>n instanceof Element).flatMap(all)],inputs=all(p.get('notification-options')).filter(n=>n.tagName==='INPUT');inputs[0].checked=false;inputs[0].onchange();assert.equal(JSON.parse(p.window.localStorage.getItem('wdc-notification-options')).connection,false);const reloaded=panel({local:p.window.localStorage});assert.equal(reloaded.run('notificationOptions.connection'),false);
});
test('login switch follows platform capability and never invokes an OS API in the panel',()=>{
 const p=panel();p.run('state.login={supported:false,enabled:false,reason:"Development install"};renderLoginSetting();');assert.equal(p.get('login-setting').hidden,false);assert.equal(p.get('start-at-login').disabled,true);assert.equal(p.get('login-help').textContent,'Development install');p.run('state.login={supported:true,enabled:true};renderLoginSetting();');assert.equal(p.get('login-setting').hidden,false);assert.equal(p.get('start-at-login').disabled,false);assert.equal(p.get('start-at-login').attrs['aria-checked'],'true');
});
test('global settings retain saved model choices and an edited draft across polled state',()=>{
 const p=panel();p.run('state.settings={maximumRequestSeconds:3600,default:{provider:"codex",model:"saved-model",reasoning:"high"},history:{mode:"keep"}};state.providers=[{id:"codex",name:"Codex"}];state.native=[];renderOwnerSettings();');
 const all=node=>[node,...(node.children??[]).filter(n=>n instanceof Element).flatMap(all)],nodes=all(p.get('owner-settings'));
 assert.equal(nodes.find(n=>n.attrs['aria-label']==='Global default model').value,'saved-model');assert.equal(nodes.find(n=>n.attrs['aria-label']==='Global default reasoning').value,'high');
 const time=nodes.find(n=>n.attrs['aria-label']==='Maximum request time');time.value='30';time.oninput();const form=p.get('owner-settings').children[0];p.run('state.settingsRevision=5;renderOwnerSettings();');assert.equal(p.get('owner-settings').children[0],form);
});
test('old failed history does not produce fresh notifications when a panel opens',()=>{
 const p=panel();p.document.hidden=true;p.run('notificationsEnabled=true;state.jobs=[{id:"old",status:"failed",pairingId:"pair",finishedAt:"2020-01-01T00:00:00Z"}];renderAttention();');assert.equal(p.alerts.length,0);
 p.run('state.failedRequests=[{id:"new",pairingId:"pair"}];notificationOptions.failed=false;renderAttention();');assert.equal(p.alerts.length,0);
});
