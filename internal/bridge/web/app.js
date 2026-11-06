import { BridgeClient } from './index.js';
const $ = id => document.getElementById(id);
let securityNoticeShown = false;
let csrf = '', controlSession = '', state, demo, demoPairID, invitation, lastState = '';
const controlStorageKey='wdc-control-session';
let connectionWatch, connectionSnapshot, refreshWork, connectionRequestCount=0;
const connectionLoads=new Map(), connectionViews=new Map();
const imagePreviews=new Map();
try { controlSession=localStorage.getItem(controlStorageKey)??''; } catch { /* Reopen the private link when browser storage is unavailable. */ }
function storeControlSession() { try { localStorage.setItem(controlStorageKey,controlSession); } catch { /* This panel still works for the current visit. */ } }
function forgetControlSession() { try { if(localStorage.getItem(controlStorageKey)===controlSession)localStorage.removeItem(controlStorageKey); } catch {} controlSession=''; }

let noticeTimer;
const labels = { awaiting_approval:'Needs approval', queued:'Queued', running:'Running', completed:'Done', cancelled:'Cancelled', timed_out:'Timed out', blocked:'Unavailable', interrupted:'Interrupted', failed:'Failed' };
function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) { if (key === 'class') node.className = value; else if (key === 'onclick') node.onclick = value; else node.setAttribute(key, value); }
  for (const child of children) if (child !== undefined && child !== null) node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  return node;
}
function icon(name, extra = '') {
  const node = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  node.setAttribute('class', `icon ${extra}`.trim()); node.setAttribute('aria-hidden', 'true'); node.setAttribute('focusable', 'false');
  const use = document.createElementNS(node.namespaceURI, 'use'); use.setAttribute('href', `/icons.svg#${name}`); node.append(use); return node;
}
for (const placeholder of document.querySelectorAll('[data-icon]')) placeholder.replaceWith(icon(placeholder.dataset.icon, placeholder.className));
document.querySelector('.skip-link').onclick = event => { event.preventDefault(); $('main-content').focus(); };
function selectWrap(select) { return el('span', {class:'select-wrap'}, select, icon('chevron')); }
for (const select of document.querySelectorAll('select')) select.replaceWith(selectWrap(select.cloneNode(true)));
function disclosure(text, attrs = {}) { return el('summary', attrs, text, icon('chevron', 'chevron')); }
function clearErrors() { $('notice').hidden = true; for (const error of document.querySelectorAll('.form-error')) error.remove(); }
function notify(error, kind = 'error') {
  clearTimeout(noticeTimer);
  const message = error instanceof Error ? error.message : String(error), dialog = [...document.querySelectorAll('dialog[open]')].at(-1);
  if (dialog && kind === 'error') {
    let target = dialog.querySelector('.form-error');
    if (!target) { target = el('p', {class:'form-error',role:'alert'}); const panel = dialog.querySelector('form:not([hidden]), #invite-result:not([hidden])') ?? dialog; (panel.querySelector('.dialog-actions') ?? dialog.querySelector('.dialog-actions')).before(target); }
    target.textContent = message; return;
  }
  $('notice-text').textContent = message; $('notice').dataset.kind = kind; $('notice').setAttribute('role', kind === 'error' ? 'alert' : 'status'); $('notice').hidden = false;
  if (kind !== 'error') noticeTimer = setTimeout(() => { $('notice').hidden = true; }, 4000);
}
function action(fn) { return async event => {
  event?.preventDefault();
  const target = event?.currentTarget, submitter = event?.submitter ?? target;
  if (target?.getAttribute('aria-busy') === 'true') return;
  const focusControl = submitter instanceof HTMLElement ? submitter : null;
  const control = submitter instanceof HTMLButtonElement ? submitter : null, spinner = icon('loader', 'spinner');
  const key = focusControl?.closest('[data-key]')?.dataset.key, actionName = focusControl?.dataset.returnAction??focusControl?.dataset.action;
  target?.setAttribute('aria-busy', 'true');
  if (control) { control.disabled = true; control.prepend(spinner); }
  try { clearErrors(); await fn(event); await refresh(true); } catch (error) { notify(error); }
  finally {
    target?.removeAttribute('aria-busy'); spinner.remove(); if (control) control.disabled = false;
    const dialog=document.querySelector('dialog[open]');
    if(document.activeElement===document.body && dialog && focusControl?.isConnected && dialog.contains(focusControl) && focusControl.getClientRects().length)focusControl.focus({preventScroll:true});
    if (document.activeElement === document.body && !document.querySelector('dialog[open]')) {
      let next = focusControl?.isConnected ? focusControl : keyedControl(key, actionName);
      if (!next?.getClientRects().length) next = keyedControl(key, 'More options') ?? $('main-content');
      if (next?.getClientRects().length) next.focus({preventScroll:true});
    }
  }
}; }
async function controlFetch(path, init = {}) {
  const response = await fetch('/api' + path, { ...init, credentials:'omit', headers: { 'Content-Type':'application/json', 'X-Bridge-CSRF':csrf, 'X-Bridge-Session':controlSession, ...init.headers } });
  if (!response.ok) { const data=await response.json(); if (response.status === 401) lock(); const error=new Error(data.error?.message ?? 'Can’t reach the local runner.');error.status=response.status;error.code=data.error?.code;throw error; }
  return response;
}
async function api(path, init = {}) { return (await controlFetch(path,init)).json(); }
async function saveArtifact(job, artifact) {
  const response=await controlFetch(`/jobs/${job.id}/artifacts/${artifact.id}`),url=URL.createObjectURL(await response.blob());
  const link=el('a',{href:url,download:artifact.name}); document.body.append(link);link.click();link.remove();
  setTimeout(()=>URL.revokeObjectURL(url),60000);
}
function releaseImagePreviews(retained) {
  for(const [key,entry] of imagePreviews) {
    if(retained?.has(key))continue;
    imagePreviews.delete(key);entry.controller.abort();
    if(entry.url)URL.revokeObjectURL(entry.url);
  }
}
function cachedImagePreview(job, artifact) {
  const key=`${job.id}/${artifact.id}`;
  if(!imagePreviews.has(key)) {
    const entry={controller:new AbortController(),url:null};
    imagePreviews.set(key,entry);
    entry.ready=controlFetch(`/jobs/${job.id}/artifacts/${artifact.id}`,{signal:entry.controller.signal})
      .then(response=>response.blob()).then(blob=>imagePreviews.get(key)===entry ? entry.url=URL.createObjectURL(blob) : null).catch(()=>null);
  }
  return imagePreviews.get(key);
}
function jobArtifact(job, artifact, card, history) {
  const isImage=artifact.mediaType.startsWith('image/');
  const save=button(isImage?'Save image':`Save ${artifact.name}`,()=>saveArtifact(job,artifact));save.prepend(icon('download'));
  const container=el('div',{class:'job-artifact'},save);
  if(!isImage)return container;
  const image=el('img',{alt:artifact.name,decoding:'async',hidden:''});
  const status=el('p',{class:'artifact-preview-status',role:'status'},'Loading image…');
  const preview=el('div',{class:'artifact-preview','aria-busy':'true'},status,image);container.prepend(preview);
  let loading=false;
  const load=async()=>{
    if(loading || !card.isConnected || history && !card.open)return;
    loading=true;
    const entry=cachedImagePreview(job,artifact),url=await entry.ready;
    if(!preview.isConnected || imagePreviews.get(`${job.id}/${artifact.id}`)!==entry)return;
    const unavailable=()=>{preview.removeAttribute('aria-busy');status.textContent='Couldn’t load image.';};
    if(!url){unavailable();return;}
    image.onload=()=>{preview.removeAttribute('aria-busy');status.hidden=true;image.hidden=false;};
    image.onerror=unavailable;image.src=url;
  };
  if(history)card.addEventListener('toggle',()=>{if(card.open)void load();});
  else queueMicrotask(load);
  return container;
}
function lock() {releaseImagePreviews();connectionWatch?.abort();connectionWatch=undefined;connectionSnapshot=undefined;connectionLoads.clear();connectionViews.clear();for(const pending of logoBlobs.values())void pending.then(url=>{if(url)URL.revokeObjectURL(url);});logoBlobs.clear();reviewDrafts.clear();forgetControlSession(); csrf = ''; for (const dialog of document.querySelectorAll('dialog[open]')) dialog.close(); document.querySelector('.skip-link').hidden = true; $('shell').hidden = true; $('locked').hidden = false; }
let currentView;
function route() {
  const view = ['apps','activity','commands','settings'].includes(location.hash.slice(1)) ? location.hash.slice(1) : 'apps';
  if (currentView && currentView !== view) { document.querySelectorAll('.app-menu[open]').forEach(menu => {menu.open=false;}); window.scrollTo({top:0,behavior:'instant'}); }
  currentView = view;
  for (const section of document.querySelectorAll('[data-page]')) section.hidden = section.dataset.page !== view;
  for (const link of document.querySelectorAll('[data-view]')) { const active = link.dataset.view === view; link.classList.toggle('active', active); if (active) link.setAttribute('aria-current', 'page'); else link.removeAttribute('aria-current'); }
}
function button(text, fn, extra = '') { return el('button', { class:'button ' + (extra.includes('primary') ? '' : 'secondary ') + extra, 'data-action':text, 'aria-label':text, onclick:action(fn) }, text); }
function tag(text, extra = '') { return el('span', {class:'tag ' + extra}, text); }
function keyedControl(key, actionName) {
  const row = [...document.querySelectorAll('[data-key]')].find(node => node.dataset.key === key);
  return [...(row?.querySelectorAll('[data-action]') ?? [])].find(node => node.dataset.action === actionName);
}
for (const [id, returnTo] of [
  ['connect-dialog', () => $($('connect-app').hidden ? 'connect-first' : 'connect-app')],
  ['command-dialog', () => editingCommand ? keyedControl(editingCommand.id, 'Edit') : $($('add-command').hidden ? 'add-first-command' : 'add-command')],
  ['command-access-dialog', () => keyedControl(accessPairID, 'Configure')],
  ['routing-dialog', () => keyedControl(routingPairID, 'Configure')]
]) $(id).addEventListener('close', () => requestAnimationFrame(() => {
  if (!document.querySelector('dialog[open]') && (document.activeElement === document.body || document.activeElement === $('main-content') || !document.activeElement?.getClientRects().length)) { const target=returnTo(); (target?.getClientRects().length ? target : $('main-content')).focus({preventScroll:true}); }
}));
function confirmAction(title, description, label, destructive = false, cancelLabel = 'Cancel') {
  const dialog = $('confirm-dialog'); $('cancel-confirm').textContent=cancelLabel; $('confirm-title').textContent = title; $('confirm-description').textContent = description; $('accept-confirm').textContent = label;
  $('accept-confirm').classList.toggle('destructive', destructive); dialog.returnValue = '';
  return new Promise(resolve => { dialog.addEventListener('close', () => resolve(dialog.returnValue === 'accept'), {once:true}); dialog.showModal(); $('cancel-confirm').focus(); });
}
$('close-confirm').onclick = $('cancel-confirm').onclick = () => $('confirm-dialog').close();
$('accept-confirm').onclick = () => $('confirm-dialog').close('accept');
// Dismiss only a click that starts and ends on the backdrop, never a drag out
// of a form or an empty area inside the dialog. Covers dynamic dialogs too.
let backdropDialog;
function outsideDialog(event, dialog) {
  const box = dialog.getBoundingClientRect();
  return event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom;
}
document.addEventListener('pointerdown', event => { backdropDialog = event.target instanceof HTMLDialogElement && outsideDialog(event,event.target) ? event.target : null; });
document.addEventListener('pointercancel', () => { backdropDialog = null; });
document.addEventListener('click', event => {
  if (event.target === backdropDialog && backdropDialog?.open && outsideDialog(event,backdropDialog)) dismissDialog(backdropDialog);
  backdropDialog = null;
});
function commandLine(command) { return command.exec.map(value => /\s|["']/.test(value) || !value ? JSON.stringify(value) : value).join(' '); }
function preserveList(root, nodes) {
  const active = document.activeElement, focused = root.contains(active), key = active?.closest('[data-key]')?.dataset.key;
  const actionName = active?.dataset.action, isSummary = active?.tagName === 'SUMMARY';
  const detailsIn = row => [...(row.matches('details') ? [row] : []), ...row.querySelectorAll('details')];
  const disclosures = new Map([...root.querySelectorAll('[data-key]')].map(row => [row.dataset.key,detailsIn(row).map(detail => detail.open)]));
  const scrolls = new Map([...root.querySelectorAll('[data-key]')].map(row => [row.dataset.key, row.querySelector('.job-output')?.scrollTop]));
  root.replaceChildren(...nodes);
  for (const row of root.querySelectorAll('[data-key]')) { for (const [index,detail] of detailsIn(row).entries()) if (disclosures.get(row.dataset.key)?.[index]) detail.open = true; const output = row.querySelector('.job-output'); if (output && scrolls.has(row.dataset.key)) output.scrollTop = scrolls.get(row.dataset.key) ?? 0; }
  if (focused && key) {
    const row = [...root.querySelectorAll('[data-key]')].find(node => node.dataset.key === key);
    const next = isSummary ? row?.querySelector('summary') : [...(row?.querySelectorAll('[data-action]') ?? [])].find(node => node.dataset.action === actionName);
    (next ?? row)?.focus({preventScroll:true});
  }
}
function providerMark(provider, small = false) {
  provider=state.programs?.find(item=>item.id===provider)?.type ?? provider;
  if(provider!=='codex' && provider!=='claude')return el('span',{class:`provider-mark${small?' small':''}`,'aria-hidden':'true'},icon('terminal'));
  return el('span',{class:`provider-mark${small ? ' small' : ''}`,'aria-hidden':'true'},el('img',{src:provider === 'claude' ? '/claude-icon.png' : '/chatgpt-icon.png',alt:'',width:'24',height:'24'}));
}
function nativeLabel(native) { return native?.execution ? 'Ready' : state.nativeChecking ? 'Checking…' : native?.installed ? 'Unavailable' : native ? 'Not installed' : 'Not checked'; }
function clientRow(provider, native) {
  let hint = native ? native.installed ? native.version ? `Installed · ${native.version}` : 'Installed' : 'Not installed' : state.nativeChecking ? 'Checking installed client…' : 'Not checked';
  if (native?.authentication === 'native_subscription') hint = 'Signed in with your subscription';
  if (native?.authentication === 'not_signed_in_or_unavailable' && (provider.integration === 'codex' || provider.id === 'codex')) hint = 'Sign in using the installed client';
  if (native?.authentication === 'api_key_not_allowed') hint = 'API sign-in is not supported';
  return el('div',{class:'client-row'},providerMark(provider.id),el('div',{class:'client-info'},el('strong',{},provider.name),el('small',{},hint)),el('span',{class:`client-status ${native?.execution ? 'ready' : ''}`},nativeLabel(native)));
}
function nativeChip(provider) {
  const native = state.native?.find(item => item.id === provider.id);
  return el('a',{class:'native-chip',href:'#settings',onclick:event => {
    event.preventDefault();location.hash='settings';
    requestAnimationFrame(()=>{route();const detail=$('provider-list').querySelector(`[data-provider=${provider.id}]`);if(detail){detail.open=true;detail.closest('.provider-item').scrollIntoView({block:'start'});detail.querySelector('summary').focus({preventScroll:true});}});
  }},providerMark(provider.id,true),el('span',{},provider.name),el('span',{class:`client-status ${native?.execution ? 'ready' : ''}`},nativeLabel(native)),icon('arrow-right'));
}
function appRow(pair) {
  const overrides = Object.values(pair.routing?.groups ?? {}), target = pair.routing?.default;
  const pending = state.jobs.filter(job => job.pairingId === pair.id && job.status === 'awaiting_approval').length+(state.permissions??[]).filter(request=>request.pairingId===pair.id).length;
  const working = state.jobs.some(job => job.pairingId === pair.id && ['queued','running'].includes(job.status));
  const status = pair.paused ? 'Paused' : pending ? pending === 1 ? 'Approval needed' : `${pending} awaiting approval` : working ? 'Working' : 'Connected';
  const grants = Object.keys(pair.actions ?? {}).length;
  const granted = state.commands.filter(command=>pair.actions?.[command.id]===command.hash).length;
 const folderCount=pair.folders?.length??0;
  const choice = el('div',{class:`connection-choice${!target && !overrides.length ? ' unconfigured' : ''}`},
    el('span',{class:'choice-label'},'Default program'),
    el('button',{type:'button',class:'connection-program','data-action':'Configure','aria-label':`Program settings for ${pair.name}`,onclick:() => openRouting(pair)},
      el('span',{class:'choice-value'},target?.provider ? providerMark(target.provider,true) : icon(target?.action ? 'terminal' : 'bridge'),target ? targetLabel(target) : overrides.length ? 'Set by group' : 'Choose a program'),icon('chevron','chevron')));
  if(overrides.length) choice.append(el('span',{class:'choice-note'},`${overrides.length} custom group${overrides.length===1?'':'s'}`));
  if(target?.provider && !(state.providersByPairing?.[pair.id] ?? state.providers).some(provider=>provider.id===target.provider && provider.execution)) choice.append(el('span',{class:'choice-note attention'},'Unavailable for this app'));
  if(target?.action && !state.commands.some(command=>command.id===target.action && command.hash===pair.actions?.[command.id])) choice.append(el('span',{class:'choice-note attention'},'Review command access'));
  const menu = el('details',{class:'app-menu'},el('summary',{class:'icon-button','aria-label':`More options for ${pair.name}`,'data-action':'More options',title:'More options'},icon('more')));
  const menuAction = (label, fn, extra='') => button(label,async () => {menu.open=false;await fn();},extra);
  const items = el('div',{class:'action-menu'},
    menuAction('Program settings',() => openRouting(pair)),
    menuAction('Connection details',() => openConnectionDetails(pair)),
    menuAction('Edit app',() => openAppAppearance(pair)),
    menuAction(pair.paused ? 'Resume requests' : 'Pause requests',() => api(`/pairings/${pair.id}`,{method:'PATCH',body:JSON.stringify({paused:!pair.paused})})),
    state.programs?.some(item=>item.type==='custom') ? menuAction('Custom AI program access',()=>openProgramAccess(pair)) : null,
    state.commands.length || grants ? menuAction('Command access',() => openCommandAccess(pair)) : null,
    menuAction('Folder access',() => openPermissions(pair)),
    menuAction('Disconnect',async () => {if(await confirmAction(`Disconnect ${pair.name}?`,'Its active requests will be cancelled. Connecting again requires a new invitation.','Disconnect',true))await api(`/pairings/${pair.id}`,{method:'PATCH',body:JSON.stringify({revoke:true})});},'danger'));
  menu.append(items);
  menu.addEventListener('toggle',() => {if(menu.open && menu.isConnected)for(const other of document.querySelectorAll('.app-menu[open]'))if(other!==menu)other.open=false;});
  const permissions = el('div',{class:'connection-choice'},el('span',{class:'choice-label'},'App access'),
    el('button',{type:'button',class:'connection-program','data-action':'Permissions','aria-label':`Permissions for ${pair.name}`,onclick:() => openAppPermissions(pair)},el('span',{class:'choice-value'},icon('shield'),'Permissions'),icon('chevron','chevron')),
    el('span',{class:'choice-note'},`${pair.automatic?'Runs automatically':'Asks each time'} · ${pair.files?'Files allowed':'Files off'}`));
  const access = el('div',{class:'connection-meta'});
  if (folderCount) access.append(button(`${folderCount} shared folder${folderCount===1?'':'s'}`,() => openPermissions(pair),'text-button'));
  if (grants) access.append(button(grants!==granted ? 'Review command access' : `${granted} allowed command${granted===1?'':'s'}`,() => openCommandAccess(pair),'text-button'));
  return el('article',{class:`app-connection${pair.paused?' paused':''}`,'data-key':pair.id,tabindex:'-1'},
    el('div',{class:'connection-header'},
      el('div',{class:'connection-identity'},appMark(pair.logoId),el('div',{class:'app-title'},el('strong',{},pair.name),el('small',{},pair.origin))),
      el('div',{class:'connection-controls'},el(pending?'a':'span',{class:`connection-status${pending?' attention':''}${working?' working':''}`,...(pending?{href:'#activity'}:{})},status),menu)),
    el('div',{class:'connection-settings'},choice,permissions),
    access.children.length ? access : null);
}
document.addEventListener('click',event => {for(const menu of document.querySelectorAll('.app-menu[open]'))if(!menu.contains(event.target))menu.open=false;});
document.addEventListener('keydown',event => {if(event.key==='Escape' && !document.querySelector('dialog[open]'))for(const menu of document.querySelectorAll('.app-menu[open]')){menu.open=false;menu.querySelector('summary').focus();}});
function jobTime(job) {
  const time=new Date(job.createdAt),today=new Date();
  return time.toLocaleDateString()===today.toLocaleDateString() ? time.toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'}) : time.toLocaleDateString([],{month:'short',day:'numeric',...(time.getFullYear()!==today.getFullYear()?{year:'numeric'}:{})});
}
function jobCard(job, history = false) {
  const pair = state.pairings.find(p => p.id === job.pairingId), kind = job.plan?.kind ?? job.request.kind, test = kind === 'connection-test', command = kind === 'command';
  const name = test ? 'Connection test' : command ? job.actionTitle || 'Command' : kind === 'image-generate' ? 'Generate image' : kind === 'image-edit' ? 'Edit image' : pair?.groups?.find(group=>group.id===job.request.group)?.name ?? 'AI request';
  const group = pair?.groups?.find(group => group.id === job.request.group)?.name;
  const clientName = !test && !command ? state.providers.find(provider=>provider.id === (job.plan?.provider ?? job.request.provider))?.name : null;
  const meta = [pair?.name !== name ? pair?.name ?? 'App' : null, clientName,group !== name ? group : null,job.turn ? `Message ${job.turn}` : null,...(history ? [] : [jobTime(job)])].filter(Boolean).join(' · ');
  const node = el(history?'details':'article',{class:history?'history-row':`job-card${job.status==='awaiting_approval'?' approval-card':''}`,'data-key':job.id,tabindex:'-1'});
  const content = history ? el('div',{class:'history-content'}) : node;
  if(history) node.append(el('summary',{},el('span',{class:`history-symbol status-${job.status}`},icon(job.status==='completed'?'check':job.status==='cancelled'?'close':'inbox')),el('div',{class:'history-title'},el('strong',{},name),el('small',{},meta)),el('span',{class:`job-status status-${job.status}`},labels[job.status]),el('time',{datetime:job.createdAt,title:new Date(job.createdAt).toLocaleString()},jobTime(job)),icon('chevron','chevron')),content);
  else node.append(el('div',{class:'job-header'},el('div',{},el('div',{class:'job-name'},name),el('div',{class:'job-meta'},meta)),el('span',{class:`job-status status-${job.status}`},labels[job.status])));
  if (job.request.prompt) content.append(el('pre', {class:'job-prompt'}, job.request.prompt));
  if (command) {
    if (job.request.arguments && Object.keys(job.request.arguments).length) content.append(el('pre', {class:'command-preview'}, Object.entries(job.request.arguments).map(([key,value]) => `${key}: ${value}`).join('\n')));
    if (job.output) content.append(el('pre', {class:'job-output'}, job.output));
    if (job.exitCode !== undefined && (job.exitCode !== 0 || job.outputTruncated)) content.append(el('p', {class:'job-detail'}, `Exit code ${job.exitCode}${job.outputTruncated ? ' · Output shortened' : ''}`));
    if (job.detail && ['blocked','failed','cancelled','timed_out','interrupted'].includes(job.status)) content.append(el('p', {class:'job-detail'}, job.detail));
  }
  else if (test && job.output && job.status === 'completed') content.append(el('p', {class:'job-detail'}, 'Connection working.'));
  else if (job.output) content.append(el('pre',{class:'job-output'},job.output));
  else if (job.status === 'blocked') content.append(el('p', {class:'job-detail'}, job.detail || 'This client is unavailable.', el('a', {href:'#settings'}, 'Details')));
  else if (job.code === 'tab_disconnected') content.append(el('p', {class:'job-detail'}, 'The requesting tab disconnected.'));
  else if (job.status === 'timed_out') content.append(el('p', {class:'job-detail'}, 'The request timed out.'));
  if (job.toolOperations?.length) content.append(el('ul',{class:'tool-operations'},...job.toolOperations.map(operation=>el('li',{},el('span',{class:'tool-operation-title'},operation.title),el('small',{},operation.status==='running'?'In progress':operation.status==='returned'?'Result returned':'Outcome unconfirmed')))));
  if (job.inputNames?.length) content.append(el('p',{class:'job-detail'},icon('document'),job.inputNames.join(' · ')));
  for (const artifact of job.artifacts ?? []) content.append(jobArtifact(job,artifact,node,history));
  if(job.status==='failed')content.append(el('p',{class:'job-detail'},job.error?.message || job.detail));
  const details = [];
  const definition = command && state.commands.find(item=>item.id===job.plan?.action && item.hash===job.plan?.actionHash);
  if(definition) details.push(['Program',definition.exec[0]]);
  if (job.execution?.name) details.push(['Executed',job.execution.name]);
  if (job.execution?.model) details.push(['Model',job.execution.model]);
  for(const operation of job.toolOperations ?? []) details.push(['App operation',`${operation.id}${operation.version ? ' · '+operation.version : ''}`]);
  if (job.execution?.version) details.push(['Version', `${job.execution.version}${job.execution.versionSource === 'configured' ? ' (configured)' : ''}`]);
  if (job.plan?.model) details.push(['Requested model',job.plan.model]);
  if (job.plan?.reasoning) details.push(['Requested reasoning',job.plan.reasoning]);
  if (details.length || job.error?.raw) content.append(el('details',{class:'job-details'},disclosure('Details'),el('dl',{},...details.flatMap(([label,value]) => [el('dt',{},label),el('dd',{},value)])),job.error?.raw ? el('pre',{class:'command-preview diagnostic'},job.error.raw) : null));
  if(command && job.status==='awaiting_approval') content.append(el('p',{class:'permission-caption'},icon('shield'),'Uses your account’s file and network access.'));
  if (job.status === 'awaiting_approval') content.append(el('div', {class:'actions'}, button('Approve', () => api(`/jobs/${job.id}/decision`, {method:'POST',body:JSON.stringify({approve:true})}), 'primary'), button('Decline', () => api(`/jobs/${job.id}/decision`, {method:'POST',body:JSON.stringify({approve:false})}))));
  else if (['queued','running'].includes(job.status)) content.append(el('div', {class:'actions'}, button('Cancel', () => api(`/jobs/${job.id}/cancel`, {method:'POST'}))));
  if(history && !content.children.length)content.append(el('p',{class:'job-detail'},job.detail || labels[job.status]));
  return node;
}
function render() {
  releaseImagePreviews(new Set(state.jobs.flatMap(job=>(job.artifacts??[]).map(artifact=>`${job.id}/${artifact.id}`))));
  renderPermissionRequests();
  renderConnectionRequests();
  const apps = state.pairings.filter(p => !p.revoked).sort((a,b) => a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id));
  const approvals = state.jobs.filter(j => j.status === 'awaiting_approval');
  const pendingCount=approvals.length+(state.permissions?.length??0);$('approval-count').textContent=pendingCount;$('approval-count').hidden=!pendingCount;
  $('runner-state').textContent = state.enabled ? 'Device ready' : 'Requests paused'; $('runner-state').dataset.status = state.enabled ? 'ready' : 'paused'; $('toggle-runner').setAttribute('aria-checked',String(state.enabled));
  $('app-list-count').textContent = apps.length || ''; $('apps-section-heading').hidden = !apps.length || !connectionRequestCount;
  $('apps-empty').hidden = !!apps.length || !!connectionRequestCount; $('connect-app').hidden = !apps.length;
  $('native-list').replaceChildren(...state.providers.map(nativeChip));
  preserveList($('app-list'),apps.map(appRow));
  const byTime=(a,b)=>b.createdAt.localeCompare(a.createdAt);
  const active=state.jobs.filter(job=>['queued','running'].includes(job.status)).sort(byTime);
  const history=state.jobs.filter(job=>!['awaiting_approval','queued','running'].includes(job.status)).sort(byTime);
  $('approvals-section').hidden=!approvals.length; $('pending-count').textContent=approvals.length;
  $('running-section').hidden=!active.length; $('history-section').hidden=!history.length;
  preserveList($('approval-jobs'),approvals.sort(byTime).map(job=>jobCard(job)));
  preserveList($('running-jobs'),active.map(job=>jobCard(job)));
  preserveList($('history-jobs'),history.map(job=>jobCard(job,true)));
  $('requests-empty').hidden=!!state.jobs.length || !!state.permissions?.length || Object.values(state.conversations??{}).some(item=>item.status==='open');
  $('clear-history').hidden=!history.length;
  renderCommands(); renderSessions(); renderUpdates();
  for (const control of document.querySelectorAll('[data-sharing]')) control.setAttribute('aria-checked',String(state.sharing?.[control.dataset.sharing] ?? control.dataset.sharing !== 'rawErrors'));
  const openProviders = new Set([...$('provider-list').querySelectorAll('details[open]')].map(node => node.dataset.provider));
  preserveList($('provider-list'),state.providers.map(p=>{
    const program=state.programs?.find(item=>item.id===p.id) ?? {id:p.id,name:p.name,type:p.id};
    const native=state.native?.find(item=>item.id===p.id);
    return el('article',{class:'provider-item','data-key':p.id,tabindex:'-1'},clientRow(p,native),
      el('p',{class:'program-summary'},`${({codex:'Codex',claude:'Claude Code',custom:'Custom WDC interface'})[program.type] ?? program.type} · ${program.executable || 'Automatically detected'}${program.profileDir ? ' · '+program.profileDir : ' · Default profile'}`),
      el('div',{class:'actions'},button('Check connection',()=>api(`/programs/${p.id}/check`,{method:'POST'})),button('Edit',()=>openAIProgram(program)),
        button('Remove',()=>removeAIProgram(program),'danger')),
      el('details',{class:'provider-details','data-provider':p.id,...(openProviders.has(p.id)?{open:''}:{})},disclosure(p.execution?'Connection details':'Availability details'),
        el('div',{class:'detail-body'},el('p',{},native?.detail || p.reason),el('p',{},`Version: ${native?.version || 'Not reported'} · Account: ${native?.account || 'Not reported'}`),
          program.type==='custom'?el('p',{},`Text and streaming${native?.sessions?', continuing conversations':''}. ${native?.files?'Accepts supplied files when the connection allows files.':'This harness does not declare file support.'} Structured output, image generation and app tools are unavailable. Runs with OS account permissions.`):null,
          p.source?el('a',{href:p.source,target:'_blank',rel:'noreferrer','data-action':'documentation'},'Official documentation',icon('external')):el('a',{href:'https://github.com/thomasmikava/WebDeviceCable/blob/main/docs/ai-programs.md',target:'_blank',rel:'noreferrer'},'WDC custom interface documentation'))));
  }));
  $('restore-ai-defaults').replaceChildren(...['codex','claude'].filter(id=>!state.programs?.some(p=>p.id===id)).map(id=>button(`Restore default ${id==='codex'?'Codex':'Claude Code'}`,async()=>{
    if(!await confirmAction('Restore automatic detection?','Connections that selected this default ID will use the restored entry. Their existing account bindings still require reconnection if the sign-in changed.','Restore default'))return;
    await api(`/program-defaults/${id}`,{method:'POST'});await api(`/programs/${id}/check`,{method:'POST'});
  })));
  $('check-clients').textContent=state.nativeChecking?'Checking…':'Check all';$('check-clients').disabled=state.nativeChecking;
  if($('routing-dialog').open){
    const pair=state.pairings.find(item=>item.id===routingPairID);
    const catalog=pair ? JSON.stringify([state.native,providersForPair(pair)]) : '';
    if(pair && catalog!==routingCatalog && (pair.routing?.revision ?? 0)===routingRevision){routingCatalog=catalog;refreshRoutingLabels(pair);}
  }
}
function applyConnectionSnapshot(next) {
  if(connectionSnapshot && connectionSnapshot.revision>(next.connectionRevision??-1))Object.assign(next,{connectionRevision:connectionSnapshot.revision,pairingRequests:connectionSnapshot.pairingRequests,launches:connectionSnapshot.launches});
  return next;
}
function refresh(force=false) {
  if(!csrf)return Promise.resolve();
  if(refreshWork)return force?refreshWork.then(()=>refresh()):refreshWork;
  refreshWork=(async()=>{
    const session=controlSession;const result=await api('/state');if(session!==controlSession || !csrf)return;const next=applyConnectionSnapshot(result);
    next.pairings=next.pairings.map(pair=>({...pair,reportedName:pair.name,name:pair.displayName||pair.name})).sort((a,b)=>a.id.localeCompare(b.id));
    if(next.securityReset && !securityNoticeShown){securityNoticeShown=true;notify('Security updated. Reconnect your apps.');}
    const serialized=JSON.stringify(next);state=next;syncIncomingRegistration();
    for(const [id,item]of connectionLoads)if(item.submitted && next.relayConnections?.some(binding=>binding.connectionId===item.info.connectionId))connectionLoads.delete(id);
    if($('runner-state').dataset.status==='offline'){$('runner-state').textContent=next.enabled?'Device ready':'Requests paused';$('runner-state').dataset.status=next.enabled?'ready':'paused';}
    const expired=[...connectionLoads.values()].some(item=>!item.expired&&!item.submitted&&item.expiresAt<Date.now());
    if(serialized!==lastState || expired){lastState=serialized;render();}
    void handleLaunches();
  })().finally(()=>{refreshWork=undefined;});
  return refreshWork;
}
function watchConnections() {
  connectionWatch?.abort();const controller=new AbortController();connectionWatch=controller;
  void(async()=>{
    let after;
    while(!controller.signal.aborted && csrf){
      try {
        const response=await api('/connection-events'+(after===undefined?'':`?after=${after}`),{signal:controller.signal});
        if(controller.signal.aborted)return;
        after=response.revision;
        if(state && response.revision>=(state.connectionRevision??-1)){
          const advanced=response.revision>(state.connectionRevision??-1);
          const changed=JSON.stringify([state.pairingRequests,state.launches])!==JSON.stringify([response.pairingRequests,response.launches]);
          connectionSnapshot=response;Object.assign(state,{connectionRevision:response.revision,pairingRequests:response.pairingRequests,launches:response.launches});
          syncIncomingRegistration();if(changed){lastState='';render();void handleLaunches();}
          if(advanced)void refresh(true).catch(()=>{});
        }
      } catch {
        if(controller.signal.aborted || !csrf)return;
        await new Promise(resolve=>{const timer=setTimeout(done,1000);function done(){clearTimeout(timer);controller.signal.removeEventListener('abort',done);resolve();}controller.signal.addEventListener('abort',done,{once:true});});
      }
    }
  })();
}

function openConnect() {
  clearErrors(); $('invite-manual-copy')?.remove(); $('invite-copy-hint').textContent='Paste it into your app. Expires in 5 minutes.';
  $('invite-form').reset(); for(const detail of $('invite-form').querySelectorAll('details'))detail.open=false;
  $('connect-start').hidden=false; $('invite-form').hidden=true; $('invite-result').hidden=true; $('connect-title').textContent='Connect an app';
  $('invite-commands-section').hidden=!state.commands.length; commandPicker($('invite-commands'),{},$('invite-command-consent')); $('connect-dialog').showModal();
}
$('connect-app').onclick=openConnect; $('connect-first').onclick=openConnect;
let reviewedRelay, configuringRelay, processingLaunch=false, relayReviewGeneration=0;
function relayInput() { const data=new FormData($('relay-form'));return {url:String(data.get('url')).replace(/\/$/,''),code:String(data.get('code')).trim()}; }
function showRelayReview(info) {
  reviewedRelay=info;
  $('relay-review').replaceChildren(el('div',{class:'connection-identity'},reviewMark(info,`relay:${info.connectionId}`),el('div',{class:'app-title'},el('strong',{},info.name),el('small',{},info.origin))),el('span',{},`Account: ${info.appUser}`),connectionReview(info,`relay:${info.connectionId}`),
    el('small',{},'This server can request work while the website is closed. Your local approvals and permissions still apply.'));
  $('relay-review').hidden=false; $('relay-submit').textContent='Approve and choose models'; $('relay-decline').hidden=false;
}
function openRelay() {
  clearErrors(); relayReviewGeneration++;reviewedRelay=undefined; $('relay-form').reset(); $('relay-review').replaceChildren(); $('relay-review').hidden=true;
  $('relay-submit').textContent='Review connection'; $('relay-decline').hidden=true; $('relay-manual').open=true; $('relay-dialog').showModal();
}
$('connect-server').onclick=openRelay;
$('close-relay').onclick=()=>$('relay-dialog').close();
$('relay-form').oninput=event=>{if(!['url','code'].includes(event.target.name))return;relayReviewGeneration++;reviewedRelay=undefined;$('relay-submit').textContent='Review connection';$('relay-review').hidden=true;$('relay-decline').hidden=true;};
$('relay-decline').onclick=action(async()=>{
  await api('/relay/decline',{method:'POST',body:JSON.stringify(relayInput())});$('relay-dialog').close();notify('Connection declined.','success');
});
$('relay-form').onsubmit=action(async()=>{
  const input=relayInput();
  if(!reviewedRelay){
    const generation=++relayReviewGeneration;
    $('relay-review').hidden=false;$('relay-review').replaceChildren(el('div',{class:'connection-loading',role:'status'},icon('loader','spinner'),el('span',{},'Loading connection details…')));
    try{const info=await api('/relay/inspect',{method:'POST',body:JSON.stringify(input)});if(generation===relayReviewGeneration && $('relay-dialog').open)showRelayReview(info);}
    catch(error){if(generation===relayReviewGeneration)$('relay-review').replaceChildren(el('p',{class:'connection-server-note',role:'alert'},'Couldn’t load the connection. Check the URL and pairing code, then try again.'));throw error;}
    return;
  }
  await pendingLogo(reviewDrafts.get(`relay:${reviewedRelay.connectionId}`));
  const result=await api('/relay/connect',{method:'POST',body:JSON.stringify({...input,expected:reviewedRelay,...reviewDecision(`relay:${reviewedRelay.connectionId}`)})});
  configuringRelay={relayUrl:input.url,connectionId:result.connectionId}; $('relay-dialog').close();
  notify('Approved. Waiting for the website to finish connecting; program settings will open automatically.','success');
});
async function handleLaunches() {
  if(processingLaunch)return;processingLaunch=true;
  try {
    if(configuringRelay){
      const binding=state.relayConnections?.find(item=>item.relayUrl===configuringRelay.relayUrl && item.connectionId===configuringRelay.connectionId);
      const pair=state.pairings.find(item=>item.id===binding?.pairingId && !item.revoked);
      if(pair?.groupsDeclared && !document.querySelector('dialog[open]')){configuringRelay=undefined;location.hash='apps';openRouting(pair);}
    }
    const intent=state.launches?.find(item=>item.kind); if(!intent || document.querySelector('dialog[open]'))return;
    if(intent.kind==='settings'){await api(`/launch-intents/${intent.requestId}/dismiss`,{method:'POST',body:'{}'});}
    if(intent.kind==='settings'){
      const binding=state.relayConnections?.find(item=>item.relayUrl===intent.relayUrl && item.connectionId===intent.connectionId);
      const pair=state.pairings.find(item=>item.id===binding?.pairingId && !item.revoked);
      if(!pair)throw new Error('This server connection is not paired with this WDC device. Connect it from the website first.');
      location.hash='apps';openRouting(pair);return;
    }
    if(!connectionLoads.has(intent.requestId)){
      const item={id:intent.requestId,origin:intent.relayUrl,loading:true,expiresAt:Date.now()+300000,input:{url:intent.relayUrl,code:intent.code}};
      connectionLoads.set(item.id,item);render();
      void inspectConnection(item);
    }
  } catch(error){notify(error);} finally {processingLaunch=false;}
}
$('show-invitation-form').onclick=()=>{$('connect-start').hidden=true;$('invite-form').hidden=false;$('connect-title').textContent='Create an invitation';$('invite-form').elements.name.focus();};
$('back-connect').onclick=()=>{$('invite-form').hidden=true;$('connect-start').hidden=false;$('connect-title').textContent='Connect an app';$('show-invitation-form').focus();};
$('close-connect').onclick=$('done-connect').onclick=$('done-connect-start').onclick=()=>$('connect-dialog').close();
$('dismiss-notice').onclick = () => { $('notice').hidden = true; };
$('invite-form').onsubmit = action(async event => {
  event.preventDefault(); const data = new FormData(event.target), origin = new URL(String(data.get('origin')).trim()).origin;
  const appId = String(data.get('appId')).trim() || new URL(origin).hostname;
  const grants = selectedCommands($('invite-commands'));
  const result = await api('/invitations', {method:'POST',body:JSON.stringify({name:data.get('name'),appId,origin,appUser:data.get('appUser'),automatic:data.get('automatic') === 'true',files:data.get('files')==='on',actions:grants,commandPermissionsAccepted:data.get('commandConsent') === 'on'})});
  invitation = result.invitation; $('invite-token').textContent = `${invitation.slice(0,8)} ··· ${invitation.slice(-8)}`; $('invite-form').hidden = true; $('invite-result').hidden = false; $('connect-title').textContent = 'Invitation ready'; $('copy-invite').replaceChildren(icon('copy'), 'Copy'); $('copy-invite').focus();
});
$('copy-invite').onclick = action(async () => {
  try { await navigator.clipboard.writeText(invitation); $('copy-invite').replaceChildren(icon('check'), 'Copied'); }
  catch {
    let field = $('invite-manual-copy');
    if (!field) { field = el('textarea', {id:'invite-manual-copy',readonly:'',rows:'3','aria-label':'Invitation',spellcheck:'false'}, invitation); $('invite-copy-hint').before(field); }
    $('invite-copy-hint').textContent = 'Copy the selected invitation into your app. Expires in 5 minutes.'; field.focus(); field.select();
  }
});
$('check-clients').onclick = action(() => api('/providers/check', {method:'POST'}));
$('check-updates').onclick = action(() => api('/updates/check', {method:'POST',body:'{}'}));
$('automatic-updates').onclick = action(() => api('/updates', {method:'PATCH',body:JSON.stringify({automatic:!state.updates?.automatic})}));
$('install-update').onclick = action(async () => {
  const active=state.updates?.stage==='waiting'||state.jobs.some(job=>['awaiting_approval','queued','running'].includes(job.status))||Object.values(state.conversations??{}).some(session=>session.status==='open')||(state.permissions?.length??0)>0;
  if(active&&!await confirmAction('End sessions and update?','Current requests and conversation context will end. Your connections and permissions stay saved.','End sessions and update',true))return;
  await api('/updates/install',{method:'POST',body:JSON.stringify({endSessions:active})});
});
function renderUpdates(){
  const update=state.updates;
  const supported=!!update?.supported,busy=['checking','downloading','installing'].includes(update?.stage);
  $('automatic-updates').disabled=!supported||update?.stage==='installing';$('automatic-updates').setAttribute('aria-checked',String(!!update?.automatic));
  $('installed-version').textContent=update?.version&&update.version!=='dev'?update.version:'Development build';
  $('build-version').textContent=update?.community?`${update.version} community test`:update?.official?`WebDeviceCable ${update.version}`:update?.version&&update.version!=='dev'?`${update.version} preview`:'Development build';
  const messages={development:'Updates are available in official installations.',community:'This test release uses manual updates. Download the next release and install it over this one; your connections stay saved.',application:'Open the WebDeviceCable app to install updates.',idle:'Checks daily. You can check anytime.',checking:'Checking for updates…',current:'WebDeviceCable is up to date.',available:`WebDeviceCable ${update?.available} is available.`,downloading:'Downloading update…',ready:'Update ready to install.',waiting:'Update ready. Waiting for sessions to finish.',authorization:'Update ready. macOS needs your permission to install.',installing:'Updating WebDeviceCable…',manual:'This update needs a manual installation.',error:update?.error??'Couldn’t check for updates. Try again.'};
  $('update-status').textContent=messages[update?.stage]??messages.development;
  $('check-updates').disabled=!supported||busy;
  $('manual-update').hidden=update?.stage!=='manual'&&!update?.community;
  $('manual-update').href=update?.community?'https://github.com/thomasmikava/WebDeviceCable/releases':'https://github.com/thomasmikava/WebDeviceCable/releases/latest';
  $('manual-update').textContent=update?.community?'View downloads':'Download update';
  $('install-update').hidden=!supported||!['available','ready','waiting','authorization'].includes(update?.stage);$('install-update').textContent=update?.stage==='waiting'?'End sessions and update':'Update now';
  $('update-progress').hidden=update?.stage!=='downloading';
  if(update?.size&&update?.downloaded){$('update-progress').max=update.size;$('update-progress').value=update.downloaded;}else{$('update-progress').removeAttribute('value');}
}
$('run-test').onclick = action(async () => {
  if (demo && !state.pairings.some(p => p.id === demoPairID && !p.revoked && !p.paused)) { await demo.close().catch(() => {}); demo = null; }
  if (!demo) { const origin = location.origin; const result = await api('/invitations', {method:'POST',body:JSON.stringify({name:'Connection test',appId:'webdevicecable.local-test',origin,appUser:'This computer',mode:'restricted',automatic:false})}); const paired = await BridgeClient.pair({baseURL:origin,invitation:result.invitation,appId:'webdevicecable.local-test',appUser:'This computer',origin}); demoPairID = paired.pairing.id; demo = new BridgeClient({baseURL:origin,connection:paired.connection,origin}); }
  await demo.connect(); await demo.submit({idempotencyKey:crypto.randomUUID(),kind:'connection-test'}); location.hash = 'activity';
});
let editingCommand, accessPairID, routingPairID, routingRevision, routingReturn, routingCatalog='';
function renderCommands() {
  $('add-command').hidden = !state.allowWebCommandEditing || !state.commands.length;
  $('add-first-command').hidden = !state.allowWebCommandEditing;
  $('commands-empty-hint').hidden = state.allowWebCommandEditing;
  $('commands-empty').hidden = !!state.commands.length;
  $('config-path').textContent = state.configPath;
  $('command-editor-hint').textContent = state.allowWebCommandEditing ? 'Web editing enabled by config.yaml.' : 'Web editing is locked. Set bridge.allowWebCommandEditing to true in this file and restart to enable it.';
  $('command-list').hidden = !state.commands.length;
  const openCommands = new Set([...$('command-list').querySelectorAll('details[open]')].map(node => node.dataset.key));
  preserveList($('command-list'), state.commands.map(command => {
    const inputs = command.arguments?.length ?? 0, program = command.exec[0].split(/[\\/]/).pop();
    const editable = command.source === 'web' && state.allowWebCommandEditing;
    return el('details', {class:'command-row','data-key':command.id,tabindex:'-1',...(openCommands.has(command.id) ? {open:''} : {})},
      el('summary', {}, el('div', {class:'command-info'}, el('strong', {}, command.title), el('small', {}, `${program}${command.session ? ' · Session' : ''}${inputs ? ` · ${inputs} input${inputs === 1 ? '' : 's'}` : ''}`)), editable ? null : tag(command.source === 'web' ? 'Locked' : 'config.yaml'), icon('chevron', 'chevron')),
      el('div', {class:'command-body'}, el('code', {class:'command-line'}, commandLine(command)), el('p', {class:'command-options'}, `${command.timeout}s timeout · ${command.maxConcurrent} at a time`),
        editable ? el('div', {class:'actions'}, button('Edit', () => openCommandEditor(command)), button('Remove', async () => { if (await confirmAction(`Remove ${command.title}?`, 'Apps will lose access to this command. Its active requests will be cancelled.', 'Remove command', true)) await api(`/commands/${command.id}`, {method:'DELETE'}); }, 'danger')) : null));
  }));
}
function selectedCommands(container) {
  return Object.fromEntries([...container.querySelectorAll('input[data-command]:checked')].map(input => [input.dataset.command, input.dataset.hash]));
}
function commandPicker(container, granted, consent) {
  const box = consent.querySelector('input'); box.checked = false;
  const update = () => { const selected = Object.keys(selectedCommands(container)).length > 0; consent.hidden = !selected; box.required = selected; if (!selected) box.checked = false; if (container.id === 'invite-commands') $('invite-access-label').textContent = selected ? 'Selected commands' : 'Restricted'; };
  container.replaceChildren(...state.commands.map(command => {
    const input = el('input', {type:'checkbox','data-command':command.id,'data-hash':command.hash}); input.checked = granted[command.id] === command.hash; input.onchange = update;
    return el('label', {class:'check-line command-choice'}, input, el('span', {}, command.title, granted[command.id] && granted[command.id] !== command.hash ? tag('Changed') : null, el('code', {class:'command-line'}, commandLine(command))));
  }));
  if (!state.commands.length) container.append(el('p', {class:'muted'}, 'No commands configured.'));
  update();
}
function openCommandAccess(pair) { clearErrors(); accessPairID = pair.id; $('command-access-context').textContent = `${pair.name} · ${pair.origin}`; commandPicker($('command-access-list'), pair.actions ?? {}, $('access-command-consent')); $('command-access-dialog').showModal(); }
$('close-command-access').onclick = () => $('command-access-dialog').close();
$('command-access-form').onsubmit = action(async event => {
  event.preventDefault(); const grants = selectedCommands($('command-access-list'));
  await api(`/pairings/${accessPairID}`, {method:'PATCH',body:JSON.stringify({actions:grants,commandPermissionsAccepted:new FormData(event.target).get('commandConsent') === 'on'})});
  await refresh(); $('command-access-dialog').close();
});
$('copy-config-path').onclick = action(async () => { await navigator.clipboard.writeText(state.configPath); notify('Configuration path copied.', 'success'); });
function addInputEditor(input = {}, isNew = false) {
  const name = el('input', {'data-field':'name',value:input.name ?? '',placeholder:'message',maxlength:'64',pattern:'[A-Za-z0-9_]+',required:'',autocomplete:'off'});
  const type = el('select', {'data-field':'type'}, ...['ascii','ascii_identifier','ascii_sentence','int','url','very_dangerous_raw_string','raw_string_multiline'].map(value => el('option', {value}, ({ascii:'Letters and numbers',ascii_identifier:'Identifier',ascii_sentence:'Simple text',int:'Number',url:'Web URL',very_dangerous_raw_string:'Text',raw_string_multiline:'Multiple lines'})[value]))); type.value = input.type ?? 'ascii';
  const defaults = el('textarea', {'data-field':'default',rows:'1'}, input.default ?? '');
  const choices = el('textarea', {'data-field':'choices',rows:'2',placeholder:'One allowed value per line'}, (input.choices ?? []).map(choice => choice.value).join('\n'));
  const required = el('input', {type:'checkbox','data-field':'required'}); required.checked = !!input.rejectNull;
  const title = el('span', {}, input.name ?? 'New input');
  const row = el('details', {class:'input-editor',...(isNew ? {open:''} : {})}, el('summary', {}, title, icon('chevron', 'chevron')),
    el('div', {class:'input-editor-body'}, el('div', {class:'form-columns'}, el('label', {}, 'Name', name), el('label', {}, 'Type', selectWrap(type))),
      el('label', {}, 'Default value', defaults), el('details', {class:'advanced'}, disclosure('Choices'), el('label', {}, 'Allowed values', choices)),
      el('label', {class:'check-line'}, required, el('span', {}, 'Required'))));
  name.oninput = () => { title.textContent = name.value || 'New input'; };
  row.originalInput = input; row.initialChoices = choices.value;
  row.querySelector('.input-editor-body').append(el('button', {type:'button',class:'text-button danger',onclick:() => { row.remove(); $('add-command-input').focus(); }}, 'Remove input'));
  $('command-inputs').append(row);
  if (isNew) name.focus();
}
function openCommandEditor(command) {
  editingCommand = command;
  clearErrors(); const form = $('command-form'); form.reset(); for (const detail of form.querySelectorAll('details')) detail.open = false; $('command-inputs').replaceChildren();
  $('command-title').textContent = command ? 'Edit command' : 'Add command';
  for (const key of ['id','title','timeout','maxConcurrent']) form.elements[key].value = command?.[key] ?? ({timeout:30,maxConcurrent:1}[key] ?? '');
  form.elements.id.readOnly = !!command;
  form.elements.program.value = command?.exec[0] ?? '';
  form.elements.version.value = command?.version ?? ''; form.elements.session.checked = !!command?.session; form.elements.idleTimeout.value = command?.session?.idleTimeout ?? 0; updateSessionFields();
  form.elements.argv.value = (command?.exec.slice(1) ?? []).join('\n'); form.initialArgv = form.elements.argv.value;
  for (const input of command?.arguments ?? []) addInputEditor(input);
  $('command-inputs').closest('details').open = !!command?.arguments?.length;
  $('command-dialog').showModal();
}
function updateSessionFields() { const enabled = $('command-form').elements.session.checked; $('session-idle-field').hidden = !enabled; $('command-form').elements.idleTimeout.disabled = !enabled; }
$('command-form').elements.session.onchange = updateSessionFields;
$('add-command').onclick = $('add-first-command').onclick = () => openCommandEditor();
$('close-command').onclick = () => $('command-dialog').close();
$('add-command-input').onclick = () => { if ($('command-inputs').children.length >= 16) { notify('A command can have up to 16 inputs.'); return; } addInputEditor({}, true); };
$('command-form').onsubmit = action(async event => {
  event.preventDefault(); const form = event.target, data = new FormData(form);
  const arguments_ = [...$('command-inputs').children].map(row => {
    const field = name => row.querySelector(`[data-field=${name}]`);
    const choices = field('choices').value === row.initialChoices ? row.originalInput.choices : field('choices').value === '' ? [] : field('choices').value.split('\n').map(value => ({value}));
    return {name:field('name').value.trim(),title:row.originalInput.title ?? '',type:field('type').value,default:field('default').value,rejectNull:field('required').checked,choices};
  });
  const argvText = String(data.get('argv'));
  const argv = editingCommand && argvText === form.initialArgv ? editingCommand.exec.slice(1) : argvText === '' ? [] : argvText.split('\n');
  const command = {id:String(data.get('id')).trim(),title:String(data.get('title')).trim(),exec:[String(data.get('program')).trim(),...argv],arguments:arguments_,timeout:Number(data.get('timeout')),maxConcurrent:Number(data.get('maxConcurrent')),version:String(data.get('version')).trim(),...(data.get('session') === 'on' ? {session:{protocol:'jsonl',idleTimeout:Number(data.get('idleTimeout'))}} : {})};
  await api(editingCommand ? `/commands/${editingCommand.id}` : '/commands', {method:editingCommand ? 'PUT' : 'POST',body:JSON.stringify(command)});
  $('command-dialog').close();
});
$('toggle-runner').onclick = action(() => api('/runner', {method:'PATCH',body:JSON.stringify({enabled:!state.enabled})}));
$('clear-history').onclick = action(async () => { if (await confirmAction('Clear request history?', 'Finished requests and their output will be removed. Active requests will stay.', 'Clear history', true)) await api('/history', {method:'DELETE'}); });
$('sign-out').onclick = action(async () => { await api('/session', {method:'DELETE'}); csrf = ''; lock(); });
window.addEventListener('hashchange', () => {if(new URLSearchParams(location.hash.slice(1)).has('key'))void boot();else route();});
window.addEventListener('focus',()=>{void refresh().catch(()=>{});});
document.addEventListener('visibilitychange',()=>{if(!document.hidden)void refresh().catch(()=>{});});
let refreshTimer,booting=false;
document.addEventListener('invalid', event => { let parent = event.target.parentElement; while (parent) { if (parent.tagName === 'DETAILS') parent.open = true; parent = parent.parentElement; } }, true);
async function boot() {
  if(booting)return;booting=true;clearInterval(refreshTimer);connectionWatch?.abort();
  try {
    const key = new URLSearchParams(location.hash.slice(1)).get('key');
    let session;
    if (key) { history.replaceState(null, '', '/'); session=await api('/session', {method:'POST',headers:{Authorization:`Bearer ${key}`}}); }
    else session=await api('/session');
    connectionSnapshot=undefined;csrf=session.csrf; controlSession=session.session; storeControlSession();
    $('locked').hidden = true; $('shell').hidden = false; document.querySelector('.skip-link').hidden = false; route(); await refresh(true); watchConnections(); initPairingRequest();
    refreshTimer=setInterval(() => { void refresh().catch(() => { $('runner-state').textContent = 'Runner offline'; $('runner-state').dataset.status = 'offline'; }); }, 1500);
  } catch { lock(); } finally {booting=false;}
}
void boot();

let editingAIProgram,programAccessPairID;
function openAIProgram(program){
  editingAIProgram=program?{...program,expectedHash:state.programHashes?.[program.id]}:null;
  const form=$('ai-program-form');form.reset();
  $('ai-program-title').textContent=program?'Edit AI program':'Add AI program';
  for(const key of ['name','type','executable','profileDir'])form.elements.namedItem(key).value=program?.[key] ?? (key==='type'?'codex':'');
  form.elements.namedItem('arguments').value=(program?.arguments ?? []).join('\n');
  form.elements.namedItem('type').disabled=!!program && ['codex','claude'].includes(program.id);
  $('reset-ai-program').hidden=!program || !['codex','claude'].includes(program.id);
  updateProgramEditor();$('ai-program-dialog').showModal();
}
function updateProgramEditor(){
  const form=$('ai-program-form'),custom=form.elements.namedItem('type').value==='custom';
  $('ai-program-args').hidden=!custom;$('ai-program-warning').hidden=!custom;
  $('ai-program-executable-help').textContent=custom?'Choose a harness that implements the WDC JSONL interface. Arguments are passed directly, without a shell.':'Leave empty for automatic detection of Codex or Claude Code. Paths are used directly, without a shell.';
  $('ai-program-profile-help').textContent=custom?'The profile path is passed as WDC_PROFILE_DIR. Your harness owns its configuration and sign-in.':'Codex uses CODEX_HOME; Claude Code uses CLAUDE_CONFIG_DIR. Sign in through that client’s own login flow. WDC never copies credentials.';
  form.elements.namedItem('executable').required=custom;
  form.elements.namedItem('executable').placeholder=custom?'Path to a WDC-compatible harness':'Automatic detection';
}
$('add-ai-program').onclick=()=>openAIProgram();
$('close-ai-program').onclick=()=>$('ai-program-dialog').close();
$('ai-program-form').elements.namedItem('type').onchange=updateProgramEditor;
$('reset-ai-program').onclick=()=>{
  const form=$('ai-program-form');for(const key of ['executable','profileDir','arguments'])form.elements.namedItem(key).value='';
  notify('Automatic detection selected. Save to apply it.','info');
};
$('ai-program-form').onsubmit=action(async()=>{
  const form=$('ai-program-form'),value=key=>form.elements.namedItem(key).value.trim();
  const program={...(editingAIProgram?{id:editingAIProgram.id}:{}),name:value('name'),type:value('type'),executable:value('executable'),profileDir:value('profileDir'),arguments:value('type')==='custom'?form.elements.namedItem('arguments').value.split('\n').filter(line=>line!==''):[]};
  const changed=editingAIProgram && ['type','executable','profileDir','arguments'].some(key=>JSON.stringify(program[key] || (key==='arguments'?[]:''))!==JSON.stringify(editingAIProgram[key] || (key==='arguments'?[]:'')));
  if(changed&&!await confirmAction('Change AI program configuration?','Affected requests and conversations will end. If the account changes, affected applications must reconnect explicitly.','Save configuration'))return;
  const saved=await api(editingAIProgram?`/programs/${editingAIProgram.id}`:'/programs',{method:editingAIProgram?'PUT':'POST',headers:editingAIProgram?.expectedHash?{'If-Match':editingAIProgram.expectedHash}:{},body:JSON.stringify(program)});
  $('ai-program-dialog').close();await api(`/programs/${saved.id}/check`,{method:'POST'});
  notify('Saved. Checking this program without starting inference.','success');
});
async function removeAIProgram(program){
  const uses=await api(`/programs/${program.id}/uses`);
  const detail=uses.length?uses.map(use=>`${use.name}: ${use.selections.join(', ')}`).join('; ')+'. These selections will stay explicitly unavailable until you reassign them in Program settings.':'No saved connection selections use this program.';
  if(!await confirmAction(`Remove ${program.name}?`,detail+' Affected requests and conversations will end.',uses.length?'Remove and leave unavailable':'Remove program',true))return;
  await api(`/programs/${program.id}?leaveUnavailable=true`,{method:'DELETE'});
}
function openProgramAccess(pair){
  programAccessPairID=pair.id;$('program-access-form').reset();
  $('program-access-context').textContent=`${pair.name} · Choose custom AI programs this connection may execute. Codex and Claude native integrations do not need command grants.`;
  $('program-access-list').replaceChildren(...(state.programs ?? []).filter(program=>program.type==='custom').map(program=>el('label',{class:'program-grant'},
    el('input',{type:'checkbox',value:program.id,...(pair.programGrants?.[program.id]===state.programHashes?.[program.id]?{checked:''}:{})}),
    el('span',{},el('strong',{},program.name),el('small',{},[program.executable,...program.arguments ?? []].join(' ')),el('small',{},program.profileDir || 'Default profile')))));
  $('program-access-dialog').showModal();
}
$('close-program-access').onclick=()=>$('program-access-dialog').close();
$('routing-program-access').onclick=()=>{
  routingReturn={pairID:routingPairID,revision:routingRevision,draft:readRoutingForm()};
  $('routing-dialog').close();openProgramAccess(state.pairings.find(pair=>pair.id===routingPairID));
};
$('program-access-form').onsubmit=action(async()=>{
  const programs=Object.fromEntries([...$('program-access-list').querySelectorAll('input:checked')].map(input=>[input.value,state.programHashes[input.value]]));
  const accepted=$('program-access-form').elements.namedItem('consent').checked;
  if(Object.keys(programs).length&&!accepted)throw new Error('Accept OS account permissions before granting custom programs.');
  await api(`/pairings/${programAccessPairID}/program-grants`,{method:'PUT',body:JSON.stringify({programs,commandPermissionsAccepted:accepted})});
  await refresh();$('program-access-dialog').close();
});
function targetLabel(target) { return target?.action ? state.commands.find(command => command.id === target.action)?.title ?? 'Unavailable command' : target?.provider ? state.providers.find(program=>program.id===target.provider)?.name ?? 'Unavailable AI program' : 'Use default'; }
function routeEditor(pair, group, current) {
  const selector = el('select', {'data-field':'target','aria-label':`Program for ${group?.name ?? 'default'}`},
    el('option',{value:''},group ? 'Inherit program' : 'Choose a program'));
  const providers = state.providersByPairing?.[pair.id] ?? state.providers;
  if (!group || group.kind === 'ai') for (const provider of providers) {
    const supported = group?.operation !== 'image' || provider.imageGeneration;
    const option = el('option',{value:`provider:${provider.id}`},provider.name + (!supported ? ' (images unavailable)' : provider.execution ? '' : ' (unavailable)'));
    option.disabled = !supported; selector.append(option);
  }
  if (group?.operation !== 'image') for (const command of state.commands) if (pair.actions?.[command.id] === command.hash) selector.append(el('option',{value:`command:${command.id}`},command.title));
  const savedValue = current?.action ? `command:${current.action}` : current?.provider ? `provider:${current.provider}` : '';
  if(savedValue && ![...selector.options].some(option=>option.value===savedValue))selector.append(el('option',{value:savedValue},targetLabel(current)+' (review access)'));
  selector.value = current?.action ? `command:${current.action}` : current?.provider ? `provider:${current.provider}` : '';
  const optionValue = (name, reset) => current?.[name] || (current && (!current.inheritOptions || current[reset]) ? '@default' : '');
  const model = el('select',{'data-field':'model','aria-label':`Model for ${group?.name ?? 'default'}`});
  const reasoning = el('select',{'data-field':'reasoning','aria-label':`Reasoning for ${group?.name ?? 'default'}`});
  model.dataset.value = optionValue('model','modelDefault'); reasoning.dataset.value = optionValue('reasoning','reasoningDefault');
  const note = el('small',{class:'route-selection'}), availability = el('p',{class:'route-note'});
  const fields = el('div',{class:'route-fields'},el('label',{},'Program',selectWrap(selector)),
    el('label',{},'Model',selectWrap(model)),el('label',{},'Reasoning',selectWrap(reasoning)));
  const row = el('details',{class:`route-editor${group ? '' : ' default-route'}`,'data-group':group?.id ?? ''},
    el('summary',{},el('span',{class:'route-label'},el('strong',{},group?.name ?? `Default for ${pair.name}`),note),icon('chevron','chevron')),
    fields,availability);
  if (!group) row.open = true;
  const reset = el('button',{type:'button',class:'text-button',onclick:() => {
    selector.value=''; model.dataset.value=''; reasoning.dataset.value=''; refreshRoutingLabels(pair);
  }},'Reset to inherited settings');
  if (group) row.append(reset);
  selector.onchange = () => {
    const next=selector.value.startsWith('provider:') ? selector.value.slice(9) : row.dataset.inheritedProvider;
    if(next!==row.dataset.provider){model.dataset.value='';reasoning.dataset.value='';notify('Program changed. Choose compatible model and reasoning settings, or keep the inherited defaults.','info');}
    refreshRoutingLabels(pair);
  };
  model.onchange = () => { model.dataset.value=model.value; refreshRoutingLabels(pair); };
  reasoning.onchange = () => { reasoning.dataset.value=reasoning.value; refreshRoutingLabels(pair); };
  return row;
}
function readRoute(row) {
  const value = row.querySelector('[data-field=target]').value;
  if (value.startsWith('command:')) return {action:value.slice(8)};
  const result = {inheritOptions:true};
  if (value.startsWith('provider:')) result.provider=value.slice(9);
  for (const name of ['model','reasoning']) {
    const choice=row.querySelector(`[data-field=${name}]`).dataset.value;
    if (choice === '@default') result[name+'Default']=true;
    else if (choice) result[name]=choice;
  }
  return Object.keys(result).length === 1 ? null : result;
}
function applyRoute(base, override) {
  if (!override) return {...base};
  if (!override.inheritOptions || override.action || override.provider && override.provider !== base.provider) base={provider:override.provider,action:override.action};
  else base={...base};
  if (override.provider) {base.provider=override.provider;delete base.action;}
  for (const field of ['model','reasoning']) if (!override.inheritOptions || override[field] || override[field+'Default']) base[field]=override[field] ?? '';
  return base;
}
function refreshRoutingLabels(pair) {
  const rows=[...$('routing-form').querySelectorAll('[data-group]')];
  const rowByID=new Map(rows.map(row=>[row.dataset.group,row]));
  const targetFor=(id,depth=0)=>{
    if(depth>8)return {};
    const group=pair.groups.find(item=>item.id===id),row=rowByID.get(id);
    const base=id ? targetFor(group?.parentId ?? '',depth+1) : {};
    return row ? applyRoute(base,readRoute(row)) : base;
  };
  const fill=(select, options, inherited, group)=>{
    const value=select.dataset.value ?? '';
    select.replaceChildren(el('option',{value:''},group ? `Inherit · ${inherited || 'client default'}` : 'Client default'),
      ...(group ? [el('option',{value:'@default'},'Native client default')] : []),
      ...options.map(([value,label])=>el('option',{value},label)));
    // An old saved setting is kept visible; we never discard it on opening.
    if(value && ![...select.options].some(option=>option.value===value))select.append(el('option',{value},value==='@default'?'Native client default':`${value} (not reported by client)`));
    select.value=value;
  };
  for(const row of rows){
    const id=row.dataset.group,group=pair.groups.find(item=>item.id===id),target=targetFor(id);
    const inherited=id?targetFor(group?.parentId ?? ''):{};
    row.dataset.provider=target.provider ?? '';row.dataset.inheritedProvider=inherited.provider ?? '';
    const native=state.native?.find(item=>item.id===target.provider),models=native?.models ?? [];
    const model=row.querySelector('[data-field=model]'),reasoning=row.querySelector('[data-field=reasoning]');
    fill(model,models.map(item=>[item.model,item.displayName || item.model]),inherited.model,group);
    const selected=models.find(item=>item.model===target.model);
    const efforts=selected?.supportedReasoningEfforts ?? (!target.model ? models.reduce((common,item)=>common===null ? item.supportedReasoningEfforts ?? [] : common.filter(effort=>item.supportedReasoningEfforts?.some(other=>other.reasoningEffort===effort.reasoningEffort)),null) : []) ?? [];
    fill(reasoning,efforts.map(item=>[item.reasoningEffort,({low:'Low',medium:'Medium',high:'High',xhigh:'Extra high',max:'Maximum',none:'None'})[item.reasoningEffort] ?? item.reasoningEffort]),inherited.reasoning,group);
    model.disabled=!!target.action || !target.provider || !!group && group.kind!=='ai';
    reasoning.disabled=model.disabled || !efforts.length && !reasoning.dataset.value;
    const source=readRoute(row);
    const custom=source ? [source.provider || source.action ? 'Program' : '',source.model || source.modelDefault ? 'Model' : '',source.reasoning || source.reasoningDefault ? 'Reasoning' : ''].filter(Boolean) : [];
    const commandRequired=!!group && group.kind!=='ai' && !!target.provider;
    const provider=providersForPair(pair).find(item=>item.id===target.provider);
    const imageCompatible=provider?.integration==='codex' || target.provider==='codex';
    const incompatible=commandRequired || group?.operation==='image' && !imageCompatible;
    const invalidEffort=!!target.reasoning && efforts.length>0 && !efforts.some(item=>item.reasoningEffort===target.reasoning);
    reasoning.setCustomValidity(invalidEffort ? 'Choose a reasoning level supported by this model, or the native client default.' : '');
    const pieces=target.action ? [targetLabel(target)] : [targetLabel(target),target.model || 'Client default model',target.reasoning ? `${target.reasoning} reasoning` : 'Client default reasoning'];
    row.querySelector('.route-selection').textContent=(target.provider || target.action ? pieces.join(' · ') : 'Choose a default program')+(incompatible ? commandRequired ? ' · Choose a command' : ' · Requires Codex' : id ? custom.length ? ` · ${custom.join(', ')} customized` : ' · Inherited' : '');
    row.querySelector('.route-selection').classList.toggle('needs-choice',incompatible || invalidEffort);
    for(const option of row.querySelector('[data-field=target]').options){
      if(!option.value.startsWith('provider:'))continue;
      const available=providersForPair(pair).find(item=>item.id===option.value.slice(9));
      if(!available)continue;
      const supported=group?.operation!=='image' || available.imageGeneration;
      option.disabled=!supported;option.textContent=available.name+(!supported?' (images unavailable)':available.execution?'':' (unavailable)');
    }
    row.querySelector('.route-note').textContent=target.provider && !provider ? 'This AI program was removed. Choose another program explicitly.' : commandRequired ? 'This workflow requires an allowed command. Choose one here or in its parent workflow.' : group?.operation==='image' && !imageCompatible ? 'This image stage requires Codex. Choose Codex here or in its workflow.' : group?.operation==='image' ? 'Model and reasoning configure the Codex turn. Its built-in image tool controls the image renderer.' : provider && !provider.execution ? provider.reason : target.provider && !efforts.length ? 'Reasoning options are not reported for this model; the native client default applies.' : '';
  }
}
function providersForPair(pair) { return state.providersByPairing?.[pair.id] ?? state.providers; }
function openRouting(pair) {
  $('routing-program-access').hidden=!state.programs?.some(item=>item.type==='custom');
  $('routing-command-access').hidden=!state.commands.length && !Object.keys(pair.actions??{}).length;
  clearErrors(); routingPairID=pair.id; routingRevision=pair.routing?.revision ?? 0; routingCatalog=JSON.stringify([state.native,providersForPair(pair)]);
  $('routing-title').textContent=`Programs and models · ${pair.name}`;
  $('routing-context').textContent=`${pair.origin} · Choose a default AI program, then customize individual workflows if needed.`;
  $('routing-default').replaceChildren(routeEditor(pair,null,pair.routing?.default));
  const append=(parent='')=>(pair.groups ?? []).filter(group=>!group.hidden && (group.parentId ?? '')===parent).map(group=>{
    const row=routeEditor(pair,group,pair.routing?.groups?.[group.id]);
    const children=append(group.id); if(children.length)row.append(el('div',{class:'workflow-stages'},...children));
    return row;
  });
  $('routing-groups').replaceChildren(el('h3',{class:'route-heading'},'Choose by workflow'),...append());
  refreshRoutingLabels(pair); $('routing-dialog').showModal();
}
$('close-routing').onclick = () => $('routing-dialog').close();
function readRoutingForm() {
  // Retired groups remain available to already-owned work and its receipts.
  const pair=state.pairings.find(item=>item.id===routingPairID);
  const groups=Object.fromEntries(Object.entries(pair?.routing?.groups ?? {}).filter(([id])=>pair.groups.find(group=>group.id===id)?.hidden));
  for(const row of $('routing-groups').querySelectorAll('[data-group]')){const target=readRoute(row);if(target)groups[row.dataset.group]=target;}
  return {default:readRoute($('routing-default').firstElementChild),groups};
}
$('routing-command-access').onclick=()=>{
  routingReturn={pairID:routingPairID,revision:routingRevision,draft:readRoutingForm()};
  $('routing-dialog').close();openCommandAccess(state.pairings.find(pair=>pair.id===routingPairID));
};
for(const accessDialog of ['command-access-dialog','program-access-dialog'])$(accessDialog).addEventListener('close',()=>{
  if(!routingReturn)return;
  if(!csrf){routingReturn=undefined;return;}
  const {pairID,revision,draft}=routingReturn;routingReturn=undefined;
  const pair=state.pairings.find(pair=>pair.id===pairID && !pair.revoked);if(!pair)return;
  const changed=(pair.routing?.revision??0)!==revision;
  openRouting(changed?pair:{...pair,routing:{...draft,revision}});
  if(changed)notify('Preferences changed in another panel. Review the current choices.');
});
$('routing-form').onsubmit = action(async () => {
  const routing=readRoutingForm();
  const live = state.jobs.some(job => job.pairingId === routingPairID && !['completed','cancelled','timed_out','blocked','interrupted','failed'].includes(job.status)) || Object.values(state.conversations ?? {}).some(session => session.pairingId === routingPairID && session.status === 'open');
  if (live && !await confirmAction('Change app preferences?','Active requests and sessions for this app will end.','Save preferences')) return;
  await api(`/pairings/${routingPairID}/routing`,{method:'PUT',body:JSON.stringify({expectedRevision:routingRevision,routing})}); $('routing-dialog').close();
});
for (const control of document.querySelectorAll('[data-sharing]')) control.onclick = action(async () => {
  const field = control.dataset.sharing, value = !state.sharing[field];
  if (field === 'rawErrors' && value && !await confirmAction('Share diagnostic errors?','Diagnostics may contain file paths or private service details. This applies to all connected apps.','Share diagnostics')) return;
  await api('/sharing',{method:'PATCH',body:JSON.stringify({[field]:value})});
});
function renderSessions() {
  const sessions = Object.values(state.conversations ?? {}).filter(session => session.status === 'open').sort((a,b) => b.updatedAt.localeCompare(a.updatedAt));
  $('active-sessions').hidden = !sessions.length;
  preserveList($('session-list'),sessions.map(session => {
    const pair = state.pairings.find(pair => pair.id === session.pairingId), group = pair?.groups?.find(group => group.id === session.group);
    return el('article',{class:'session-row','data-key':session.id,tabindex:'-1'},el('div',{},el('strong',{},group?.name ?? targetLabel(session.plan)),el('small',{},`${pair?.name ?? 'App'} · ${session.turns ? `${session.turns} message${session.turns === 1 ? '' : 's'}` : 'Not started'}`)),button('End session',async () => {if(await confirmAction('End this session?','Its context will be discarded and any active message will be cancelled.','End session')) await api(`/conversations/${session.id}`,{method:'DELETE'});}));
  }));
}

// Requests enter through an authenticated popup, then appear in every local panel.
let incomingPairing;
function initPairingRequest() {
  const requestId = new URLSearchParams(location.hash.slice(1)).get('connect-request');
  if (!/^[a-f0-9]{64}$/.test(requestId ?? '') || !window.opener) return;
  const opener = window.opener;
  let sending = false, registered = false, delivered = false;
  const announce = () => { if (!incomingPairing) opener.postMessage({type:'wdc:pairing-ready',requestId,connectionReviewVersion:1,filePermissionsVersion:1},'*'); };
  const timer = setInterval(announce,500); announce();
  const stop = () => {clearInterval(timer);clearInterval(poll);window.removeEventListener('message',receive);};
  const expires = setTimeout(stop,300000);
  async function receive(event) {
    if (event.source !== opener || event.data?.requestId !== requestId) return;
    if (event.data.type === 'wdc:pairing-failed' && incomingPairing?.origin === event.origin) { stop();clearTimeout(expires);notify('Connection could not finish. Try again from the website.');return; }
    if (event.data.type === 'wdc:pairing-connected' && incomingPairing?.origin === event.origin) {
      stop();clearTimeout(expires);history.replaceState(null,'','#apps');notify('App connected','success');return;
    }
    if (incomingPairing || event.data.type !== 'wdc:pairing-request' || !validWebsite(event.origin)) return;
    const {appId,name,appUser,logoURL,requestedPermissions} = event.data;
    if (![appId,name,appUser].every(value => typeof value === 'string' && value.trim()) || appId.length > 100 || name.length > 100 || appUser.length > 200) return;
    incomingPairing = {requestId,opener,origin:event.origin,appId,name,appUser,registered:()=>{registered=true;},cancel:()=>{delivered=true;opener.postMessage({type:'wdc:pairing-rejected',requestId},event.origin);stop();clearTimeout(expires);}};clearInterval(timer);
    const item={id:requestId,name,origin:event.origin,loading:true,expiresAt:Date.now()+300000};connectionLoads.set(requestId,item);render();
    const register=async()=>{
      item.loading=true;item.error=undefined;render();
      try{await api('/pairing-requests',{method:'POST',body:JSON.stringify({id:requestId,origin:event.origin,appId,name,appUser,logoURL,requestedPermissions})});registered=true;connectionLoads.delete(requestId);await refresh();}
      catch(error){if(connectionLoads.get(requestId)!==item)return;item.loading=false;item.error=error.message;item.retry=register;render();}
    };
    void register();
  }
  const poll = setInterval(async () => {
    if(!registered || delivered || sending) return;
    sending = true;
    try {
      const result = await api(`/pairing-requests/${requestId}`);
      if(result.status === 'approved') {delivered=true;opener.postMessage({type:'wdc:pairing-approved',requestId,invitation:result.invitation},incomingPairing.origin);}
      if(result.status === 'rejected') {delivered=true;opener.postMessage({type:'wdc:pairing-rejected',requestId},incomingPairing.origin);stop();}
    } catch(error){notify(error);stop();} finally{sending=false;}
  },500);
  window.addEventListener('message',receive);
  window.addEventListener('pagehide',() => {if(registered && !delivered) void api(`/pairing-requests/${requestId}/decision`,{method:'POST',keepalive:true,body:JSON.stringify({approve:false})}).catch(()=>{});},{once:true});
}
function validWebsite(origin) {try{const url=new URL(origin);return url.origin===origin&&(url.protocol==='https:'||url.protocol==='http:'&&['localhost','127.0.0.1','[::1]'].includes(url.hostname));}catch{return false;}}
const reviewDrafts=new Map(), logoBlobs=new Map();
function appMark(id) {
  const mark=el('span',{class:'app-mark','aria-hidden':'true'},icon('app'));
  if(id) {
    if(!logoBlobs.has(id))logoBlobs.set(id,controlFetch(`/logos/${id}`).then(response=>response.blob()).then(blob=>URL.createObjectURL(blob)).catch(()=>{logoBlobs.delete(id);return null;}));
    void logoBlobs.get(id).then(url=>{if(url)mark.replaceChildren(el('img',{src:url,alt:''}));});
  }
  return mark;
}
function reviewDraft(info,key) {
  if(!reviewDrafts.has(key))reviewDrafts.set(key,{automatic:info.requestedPermissions?.automaticApprovals===true,files:info.requestedPermissions?.files===true,logoId:info.logoId??'',initialURL:info.logoURL??'',loaded:false});
  return reviewDrafts.get(key);
}
function reviewMark(info,key) {
  const draft=reviewDraft(info,key);
  draft.heading=appMark(draft.logoId);
  if(!draft.loaded && draft.initialURL) {
    draft.loaded=true;
    draft.loadingPromise=api('/logos',{method:'POST',body:JSON.stringify({url:draft.initialURL,origin:info.origin})}).then(result=>{
      draft.logoId=result.logoId;
      const mark=appMark(result.logoId);draft.heading.replaceWith(mark);draft.heading=mark;
    }).catch(()=>{});
  }
  return draft.heading;
}
function pendingLogo(draft) {return draft?.pending??(!draft?.edited?draft?.loadingPromise:undefined);}
function reviewDecision(key) {const draft=reviewDrafts.get(key);return {automatic:draft?.automatic??false,files:draft?.files??false,logoId:draft?.logoId??''};}
function connectionReview(info,key) {
  const draft=reviewDraft(info,key),automatic=el('input',{type:'checkbox','aria-label':`Approve requests automatically for ${info.name}`,'data-action':'Automatic approvals'});automatic.checked=draft.automatic;
  automatic.onchange=()=>{draft.automatic=automatic.checked;};
  const files=el('input',{type:'checkbox','aria-label':`Allow files for ${info.name}`,'data-action':'File access'});files.checked=draft.files;files.onchange=()=>{draft.files=files.checked;};
  return el('div',{class:'connection-review'},el('label',{class:'check-line'},automatic,el('span',{},'Run requests automatically',el('small',{},'Run without asking for approval each time.'))),el('label',{class:'check-line'},files,el('span',{},'Send and receive files',el('small',{},'This app can supply images and documents for its requests and receive files those requests produce.'))),el('small',{class:'muted'},'Computer folders: No access. Command access: Off.'));
}

function openAppPermissions(pair,initialView='overview') {
  clearErrors();
  const dialog=makeDialog('permissions-dialog',`Permissions · ${pair.name}`),automatic=el('input',{type:'checkbox'}),files=el('input',{type:'checkbox'});
  dialog.dataset.key=pair.id;
  dialog.querySelector('[aria-label="Close"]').dataset.returnAction='Permissions';
  const folders=(pair.folders??[]).map(folder=>({...folder}));
  const folderNavigation={path:''};
  const folderKey=()=>JSON.stringify(folders.map(({id,path,write,automatic})=>({id,path,write:!!write,automatic:!!automatic})));
  const originalAutomatic=!!pair.automatic,originalFiles=!!pair.files,originalFolders=folderKey();
  automatic.checked=originalAutomatic;files.checked=originalFiles;
  let saving=false,confirming=false,saveState='draft',submission;
  const expectedRevision=pair.permissionsRevision??0;
  const dirty=()=>automatic.checked!==originalAutomatic||files.checked!==originalFiles||folderKey()!==originalFolders;
  const heading=dialog.querySelector('.dialog-heading'),back=el('button',{type:'button',class:'icon-button permission-back',hidden:'','aria-label':'Back to Permissions'},icon('arrow-left'));
  heading.prepend(back);
  const setView=(title,view,goBack)=>{
    dialog.dataset.view=view;dialog.setAttribute('aria-label',`${title} · ${pair.name}`);heading.querySelector('h2').textContent=`${title} · ${pair.name}`;
    back.hidden=!goBack;back.setAttribute('aria-label',view==='choose'?'Back to Folder access':'Back to Permissions');back.onclick=()=>goBack?.();dialog.permissionBack=goBack;
  };
  dialog.permissionView=setView;
  dialog.requestDismiss=async()=>{
    if(saving)return;
    if(confirming){
      if(!$('confirm-dialog').open){await confirming;if(dialog.open)return dialog.requestDismiss();}
      return;
    }
    if(!dirty()){closeDraft();return;}
    confirming=saveState==='unknown'
      ?confirmAction('Close without confirming?','These changes may already be saved. Closing won’t undo permissions that were applied.','Close anyway',false,'Keep draft')
      :saveState==='conflict'
        ?confirmAction('Close this draft?','Closing this draft won’t change the permissions currently saved in WDC.','Close draft',false,'Keep draft')
        :confirmAction('Discard unsaved changes?','Your permission and folder changes haven’t been saved.','Discard changes',true,'Keep editing');
    try {
      if(await confirming&&dialog.open)closeDraft();
    } finally {confirming=false;}
  };
  const navigateBackOrDismiss=()=>{
    if(saving)return;
    if(confirming){void dialog.requestDismiss();return;}
    if(dialog.permissionBack)dialog.permissionBack();else void dialog.requestDismiss();
  };
  dialog.addEventListener('cancel',event=>{event.preventDefault();navigateBackOrDismiss();});
  dialog.addEventListener('keydown',event=>{
    if(event.key!=='Escape'||event.defaultPrevented||event.target instanceof HTMLSelectElement)return;
    event.preventDefault();event.stopPropagation();navigateBackOrDismiss();
  });
  const beforeUnload=event=>{if(dirty()||saving){event.preventDefault();event.returnValue='';}};
  const closeDraft=()=>{window.removeEventListener('beforeunload',beforeUnload);dialog.close();};
  window.addEventListener('beforeunload',beforeUnload);
  dialog.addEventListener('close',()=>window.removeEventListener('beforeunload',beforeUnload),{once:true});
  const warning=el('p',{class:'permission-revocation-note',hidden:''},'Turning file access off stops this app’s active file requests and conversations. Files already shared stay shared.');
  const unsaved=el('span',{class:'folder-unsaved',role:'status',hidden:''},'Unsaved changes');
  const folderSummary=el('small');
  const readSaved=()=>api(`/pairings/${pair.id}`,{signal:AbortSignal.timeout(5000)}).then(current=>({...current,reportedName:current.name,name:current.displayName||current.name}));
  const matchesDraft=current=>{
    const actual=current.folders??[],wanted=submission.desired.folders;
    return !!current.automatic===submission.desired.automatic&&!!current.files===submission.desired.files&&
      current.paused===pair.paused&&current.revoked===pair.revoked&&actual.length===wanted.length&&
      wanted.every((folder,index)=>actual[index].path===folder.path&&(!folder.id||actual[index].id===folder.id)&&!!actual[index].write===!!folder.write&&!!actual[index].automatic===!!folder.automatic);
  };
  const saved=async()=>{
    closeDraft();notify('Permissions saved.','success');
    try{await refresh(true);}catch{notify('Permissions saved. Refresh WDC to update the connection card.','info');}
    if(!document.querySelector('dialog[open]'))keyedControl(pair.id,'Permissions')?.focus({preventScroll:true});
  };
  const reconcile=async()=>{
    try {
      const current=await readSaved();
      if(!dialog.open)return;
      if(current.permissionSaveId===submission.payload.permissionSaveId&&matchesDraft(current)){await saved();return;}
      saveState=(current.permissionsRevision??0)!==expectedRevision?'conflict':'unknown';
    }catch{saveState='unknown';}
  };
  // One save ID is retained until the outcome is known. An explicit retry sends
  // that same immutable payload; reads never silently repeat a permission write.
  const perform=async(task,control)=>{
    if(saving)return;
    clearErrors();
    saving=true;dialog.setAttribute('aria-busy','true');
    const disabled=new Map([...dialog.querySelectorAll('button,input,select')].map(control=>[control,control.disabled]));
    for(const input of disabled.keys())input.disabled=true;
    const spinner=icon('loader','spinner');control.prepend(spinner);
    try{await task();}catch(error){notify(error);}
    finally{spinner.remove();saving=false;dialog.removeAttribute('aria-busy');for(const [input,value]of disabled)input.disabled=value;changed();}
  };
  const save=el('button',{type:'button',class:'button primary','data-action':'Save changes','aria-label':'Save changes'},'Save changes');
  save.onclick=()=>{
    if(saving||saveState==='conflict'||!dirty())return;
    if(!submission){
      const changes={expectedPermissionsRevision:expectedRevision,permissionSaveId:crypto.randomUUID().replaceAll('-','')+crypto.randomUUID().replaceAll('-','')};
      if(automatic.checked!==originalAutomatic)changes.automatic=automatic.checked;
      if(files.checked!==originalFiles)changes.files=files.checked;
      if(folderKey()!==originalFolders)changes.folders=folders.map(folder=>({...folder}));
      submission={payload:changes,desired:{automatic:automatic.checked,files:files.checked,folders:folders.map(folder=>({...folder}))}};
    }
    void perform(async()=>{
      try{
        const result=await api(`/pairings/${pair.id}`,{method:'PATCH',body:JSON.stringify(submission.payload),signal:AbortSignal.timeout(10000)});
        if(!dialog.open)return;
        if(result.permissionSaveId===submission.payload.permissionSaveId&&matchesDraft(result))await saved();else saveState='conflict';
      }catch(error){
        if(['permissions_changed','permission_save_conflict'].includes(error.code)){saveState='conflict';return;}
        if(error.code&&error.status>=400&&error.status<500&&error.status!==408){submission=undefined;saveState='draft';throw error;}
        saveState='unknown';await reconcile();
      }
    },save);
  };
  const recoveryText=el('p');
  const check=el('button',{type:'button',class:'button secondary'},'Check saved permissions');
  check.onclick=()=>void perform(reconcile,check);
  const reload=el('button',{type:'button',class:'button secondary'},'Reload saved permissions');
  reload.onclick=()=>void perform(async()=>{
    if(!await confirmAction('Reload saved permissions?','This will replace your draft with the currently saved settings.','Reload permissions',false,'Keep draft'))return;
    const current=await readSaved();if(!dialog.open)return;
    closeDraft();openAppPermissions(current);
  },reload);
  const recovery=el('div',{class:'permission-recovery',role:'alert',hidden:''},recoveryText,el('div',{class:'actions'},check,reload));
  save.dataset.returnAction='Permissions';
  const cancel=button('Cancel',()=>dialog.requestDismiss());cancel.dataset.returnAction='Permissions';
  const footer=el('div',{class:'permission-footer'},recovery,el('div',{class:'dialog-actions'},unsaved,cancel,save));
  const changed=()=>{
    save.disabled=saving||!dirty()||saveState==='conflict';unsaved.hidden=!dirty();warning.hidden=!originalFiles||files.checked;
    const label=saveState==='unknown'?'Retry save':'Save changes';save.textContent=label;save.setAttribute('aria-label',label);
    unsaved.textContent=saveState==='unknown'?'Save not confirmed':saveState==='conflict'?'Saved settings changed':'Unsaved changes';
    recovery.hidden=saveState==='draft';check.hidden=saveState!=='unknown';reload.hidden=saveState!=='conflict';
    recoveryText.textContent=saveState==='unknown'?'Couldn’t confirm whether your changes were saved. Your edits are kept; check the saved permissions or retry this save.':'Permissions changed while this draft was open. Reload the saved settings before making changes.';
    for(const input of dialog.querySelectorAll('[data-permission-edit]'))input.disabled=saving||saveState!=='draft'||input.dataset.permissionEdit==='add-folder'&&folders.length>=16;
    folderSummary.textContent=folders.length?`${folders.length} folder${folders.length===1?'':'s'} selected`:'Choose folders this app can read or edit.';
  };
  automatic.dataset.permissionEdit=files.dataset.permissionEdit='';
  automatic.onchange=files.onchange=changed;
  const clearView=()=>{clearErrors();for(const panel of dialog.querySelectorAll(':scope > .dialog-body, :scope > .dialog-actions, :scope > .permission-footer'))panel.remove();};
  const showOverview=()=>{
    clearView();setView('Permissions','overview');dialog.append(overview,footer);changed();folderAccess.focus({preventScroll:true});
  };
  const showFolders=()=>{
    renderFolderAccess(dialog,pair,folders,footer,changed,showOverview,clearView,folderNavigation);
    back.focus({preventScroll:true});
  };
  const folderAccess=el('button',{type:'button',class:'connection-program permission-folder-access','aria-label':'Folder access',onclick:showFolders},el('span',{class:'permission-folder-label'},el('span',{class:'choice-value'},icon('folder'),'Folder access'),folderSummary),icon('chevron','chevron'));
  const overview=el('div',{class:'dialog-body app-permissions-body'},
    el('label',{class:'check-line permission-option'},automatic,el('span',{},'Run requests automatically',el('small',{},'Run this app’s requests within its allowed access without asking each time.'))),
    el('label',{class:'check-line permission-option'},files,el('span',{},'Send and receive files',el('small',{},'Share images and documents with the AI, and receive files it creates. This does not grant access to folders on your computer.'))),warning,folderAccess);
  if(initialView==='folders')showFolders();else showOverview();
  dialog.showModal();
}

function openAppAppearance(pair) {
  const dialog=makeDialog('app-appearance-dialog','Edit app'),original=pair.logoId??'';
  let selected=original,previewedURL='',busy=false,saving=false,closed=false;
  const controller=new AbortController();
  const preview=el('div',{class:'logo-preview',role:'img','aria-label':`${selected?'Current logo':'No logo'} for ${pair.name}`},appMark(selected));
  const caption=el('small',{},selected?'Current logo':'No logo');
  const status=el('p',{class:'logo-status',role:'status',hidden:''});
  const error=el('p',{class:'logo-error',role:'alert',hidden:''});
  const name=el('input',{type:'text',id:'app-display-name',required:'',maxlength:'100',autocomplete:'off','aria-describedby':'app-name-hint'});name.value=pair.name;
  const nameHint=el('small',{id:'app-name-hint',class:'field-hint'},'Only changes how this app appears in WDC.');
  const file=el('input',{type:'file',accept:'image/png,image/jpeg,image/gif,image/svg+xml',hidden:'',tabindex:'-1','aria-label':'Choose logo image'});
  const url=el('input',{type:'url',id:'logo-image-url',placeholder:'https://example.com/logo.png',maxlength:'2048','aria-describedby':'logo-url-hint'});
  function message(text,isError=false) {
    status.hidden=isError||!text;status.textContent=isError?'':text;
    error.hidden=!isError;error.textContent=isError?text:'';
  }
  function updateControls() {
    const unpreviewed=Boolean(url.value.trim())&&url.value.trim()!==previewedURL;
    const nextName=name.value.trim();
    const nameError=!nextName?'Enter a name.':new TextEncoder().encode(nextName).length>100?'Use a shorter name.':/[\u0000-\u001f\u007f-\u009f]/u.test(nextName)?'Use a single-line name.':'';
    name.disabled=busy;name.setCustomValidity(nameError);
    nameHint.textContent=nameError||'Only changes how this app appears in WDC.';
    nameHint.classList.toggle('invalid',Boolean(nameError));
    upload.disabled=busy;url.disabled=busy;load.disabled=busy||!url.value.trim();
    remove.hidden=!selected;remove.disabled=busy;
    save.disabled=busy||(selected===original&&nextName===pair.name)||unpreviewed||Boolean(nameError);
    dialog.querySelector('.dialog-heading button').disabled=saving;
    dialog.setAttribute('aria-busy',String(busy));
  }
  name.oninput=()=>updateControls();
  function showPreview(id) {
    selected=id;preview.replaceChildren(appMark(id));
    caption.textContent=id?(id===original?'Current logo':'New logo'):'No logo';
    preview.setAttribute('aria-label',`${caption.textContent} for ${pair.name}`);
  }
  async function choose(work,source) {
    if(busy||closed)return;
    const focusControl=document.activeElement;
    busy=true;message('Loading image…');updateControls();
    try {
      const result=await work();if(closed)return;
      showPreview(result.logoId);previewedURL=source;url.value=source;
      message(selected===original?'This is already the current logo.':'Preview updated. Save changes to apply.');
    } catch(failure) {
      if(!closed)message(failure.message,true);
    } finally {
      busy=false;
      if(!closed){updateControls();if(document.activeElement===document.body&&focusControl instanceof HTMLElement&&dialog.contains(focusControl))focusControl.focus({preventScroll:true});}
    }
  }
  const upload=el('button',{type:'button',class:'button secondary',onclick:()=>file.click()},'Upload image');
  file.onchange=()=>{
    const image=file.files?.[0];file.value='';if(!image)return;
    if(image.size>262144){message('Choose an image smaller than 256 KiB.',true);return;}
    void choose(()=>api('/logos/upload',{method:'POST',signal:controller.signal,headers:{'Content-Type':image.type||'application/octet-stream'},body:image}),'');
  };
  const load=el('button',{type:'submit',class:'button secondary'},'Preview');
  const linkForm=el('form',{class:'logo-link-form'},el('label',{for:'logo-image-url'},'Or use an image URL'),el('div',{class:'logo-link-input'},url,load),el('small',{id:'logo-url-hint'},'Paste a link directly to the image.'));
  linkForm.onsubmit=event=>{
    event.preventDefault();if(!url.reportValidity())return;
    const source=url.value.trim();if(!source)return;
    void choose(()=>api('/logos',{method:'POST',signal:controller.signal,body:JSON.stringify({url:source,origin:pair.origin})}),source);
  };
  url.oninput=()=>{message(url.value.trim()&&url.value.trim()!==previewedURL?'Preview the image before saving.':'');updateControls();};
  const remove=el('button',{type:'button',class:'text-button danger',onclick:()=>{
    showPreview('');url.value='';previewedURL='';message('Logo removed from preview. Save changes to apply.');updateControls();(save.disabled?upload:save).focus();
  }},'Remove logo');
  const cancel=el('button',{type:'button',class:'button secondary',onclick:()=>{if(!saving)dialog.close();}},'Cancel');
  const save=el('button',{type:'button',class:'button primary',onclick:async()=>{
    if(save.disabled||closed)return;
    busy=true;saving=true;message('Saving…');updateControls();cancel.disabled=true;
    try {
      const displayName=name.value.trim()===(pair.reportedName??pair.name)?'':name.value.trim();
      await api(`/pairings/${pair.id}`,{method:'PATCH',body:JSON.stringify({displayName,logoId:selected})});
      dialog.close();await refresh(true);
    } catch(failure) {if(!closed)message(failure.message,true);}
    finally {busy=false;saving=false;cancel.disabled=false;if(!closed)updateControls();}
  }},'Save changes');
  dialog.addEventListener('close',()=>{closed=true;controller.abort();});
  dialog.addEventListener('cancel',event=>{if(saving)event.preventDefault();});
  dialog.addEventListener('click',event=>{if(saving&&event.target===dialog)event.stopPropagation();});
  dialog.append(el('div',{class:'dialog-body logo-body'},
    el('div',{class:'app-name-field'},el('label',{for:'app-display-name'},'Display name'),name,nameHint),
    el('div',{class:'logo-current'},preview,el('div',{class:'logo-identity'},el('strong',{},'Logo'),caption,upload),file),
    el('p',{class:'logo-format-hint'},'PNG, JPG, GIF or SVG · up to 256 KiB'),linkForm,status,error),
    el('div',{class:'dialog-actions logo-actions'},remove,el('div',{class:'logo-save-actions'},cancel,save)));
  updateControls();dialog.showModal();name.focus();
}
function syncIncomingRegistration() {
  if(!incomingPairing || !state?.pairingRequests?.some(item=>item.id===incomingPairing.requestId))return;
  incomingPairing.registered();connectionLoads.delete(incomingPairing.requestId);
}
function visibleConnectionRequests() {
  const paired=new Set(state.pairings.map(pair=>pair.id));
  const requests=(state.pairingRequests??[]).filter(item=>!item.pairingId||!paired.has(item.pairingId)).map(item=>item.status==='approved'?{...item,submitted:true}:item);
  const ids=new Set(requests.map(item=>item.id));
  const relayApprovals=new Set((state.pairingRequests??[]).filter(item=>item.status==='approved'&&item.connectionId).map(item=>`${item.relayUrl}:${item.connectionId}`));
  for(const item of connectionLoads.values())if(!ids.has(item.id)&&!relayApprovals.has(`${item.input?.url}:${item.info?.connectionId}`)){if(!item.submitted && item.expiresAt<Date.now()){item.expired=true;item.loading=false;item.error='This request expired. Start a new connection from the app.';}requests.push(item.info?{...item,...item.info,id:item.id}:item);ids.add(item.id);}
  for(const intent of state.launches??[])if(intent.kind!=='settings'&&!ids.has(intent.requestId))requests.push({id:intent.requestId,origin:intent.origin||intent.relayUrl,loading:true});
  return requests;
}
function renderConnectionRequests() {
  const requests=visibleConnectionRequests();connectionRequestCount=requests.length;
  $('connection-requests').hidden=!requests.length;
  $('connection-request-count').textContent=requests.length;$('connection-request-count').hidden=!requests.length;
  document.title=requests.length?`(${requests.length}) WebDeviceCable`:'WebDeviceCable';
  const previous=new Map(connectionViews);
  const rows=requests.map(request=>{
    const signature=JSON.stringify(request),cached=connectionViews.get(request.id);
    if(cached?.signature===signature)return cached.node;
    const ready=!request.loading&&!request.error&&!request.submitted && !!request.name;
    const title=request.name??'New connection';
    const heading=el('div',{class:'connection-request-heading'},ready?reviewMark(request,request.id):appMark(request.logoId??reviewDrafts.get(request.id)?.logoId),el('div',{class:'connection-request-identity'},el('h3',{},title),el('small',{},request.origin)));
    const card=el('article',{class:`connection-request${ready?'':' connection-pending'}`,'data-key':request.id,tabindex:'-1','aria-label':`${title} connection request`},heading);
    connectionViews.set(request.id,{signature,node:card,name:request.name});
    if(!ready){
      card.append(el('div',{class:'connection-loading',role:request.error?'alert':'status'},icon(request.error?'inbox':'loader',request.error?'':'spinner'),el('span',{},request.error??(request.submitted?'Connecting…':'Loading connection details…'))));
      if(request.submitted&&!request.error)card.append(el('div',{class:'connection-placeholder','aria-hidden':'true'},...Array.from({length:2},()=>el('div',{},el('span'),el('span')))));
      if(request.error){card.append(el('div',{class:'connection-request-actions'},button('Dismiss',()=>dismissConnection(request)),request.expired?null:button('Try again',()=>request.input?inspectConnection(connectionLoads.get(request.id)):request.retry())));}
      return card;
    }
    const details=el('details',{class:'connection-details'},disclosure('Connection details'),el('dl',{},el('dt',{},'Account'),el('dd',{},request.appUser),el('dt',{},'App ID'),el('dd',{},request.appId)));
    card.append(connectionReview(request,request.id));
    if(request.input)card.append(el('p',{class:'connection-server-note'},'This app can send requests even when its website is closed.'));
    card.append(el('div',{class:'connection-request-footer'},details,el('div',{class:'actions'},button('Decline',()=>declineConnection(request)),button('Connect',()=>approveConnection(request),'primary'))));
    if(cached?.node.querySelector('details')?.open)details.open=true;
    return card;
  });
  const root=$('connection-request-list'),active=document.activeElement,key=active?.closest('[data-key]')?.dataset.key,actionName=active?.dataset.action;
  const retained=new Set(rows);for(const node of [...root.children])if(!retained.has(node))node.remove();
  let cursor=root.firstElementChild;for(const row of rows){if(row===cursor)cursor=cursor.nextElementSibling;else root.insertBefore(row,cursor);}
  const ids=new Set(requests.map(item=>item.id));for(const id of connectionViews.keys())if(!ids.has(id))connectionViews.delete(id);
  if(key && !active.isConnected){const row=connectionViews.get(key)?.node;if(row)([...row.querySelectorAll('[data-action]')].find(node=>node.dataset.action===actionName)??row).focus({preventScroll:true});}
  const arrived=requests.find(request=>!previous.has(request.id)||request.name && previous.get(request.id)?.name!==request.name);
  if(arrived)$('connection-request-announcement').textContent=arrived.submitted?`${arrived.name} is connecting.`:arrived.name?`${arrived.name} wants to connect.`:'A new connection request is loading.';
}
async function inspectConnection(item) {
  item.loading=true;item.error=undefined;if(state)render();
  try{
    // Show the card before inspecting the relay or dismissing its launch notification.
    await api(`/launch-intents/${item.id}/dismiss`,{method:'POST',body:'{}'});
    const info=await api('/relay/inspect',{method:'POST',body:JSON.stringify(item.input)});
    if(connectionLoads.get(item.id)!==item)return;
    item.info=info;item.loading=false;
  }catch(error){if(connectionLoads.get(item.id)===item){item.loading=false;item.error=error.message;}}
  if(state)render();
}
function dismissConnection(request) {if(incomingPairing?.requestId===request.id)incomingPairing.cancel();connectionLoads.delete(request.id);if(state)render();}
async function declineConnection(request) {
  if(request.input){await api('/relay/decline',{method:'POST',body:JSON.stringify(request.input)});connectionLoads.delete(request.id);}
  else {await api(`/pairing-requests/${request.id}/decision`,{method:'POST',body:JSON.stringify({approve:false})});if(incomingPairing?.requestId===request.id)incomingPairing.cancel();}
}
async function approveConnection(request) {
  await pendingLogo(reviewDrafts.get(request.id));
  if(!request.input){await api(`/pairing-requests/${request.id}/decision`,{method:'POST',body:JSON.stringify({approve:true,...reviewDecision(request.id)})});return;}
  const item=connectionLoads.get(request.id);
  const result=await api('/relay/connect',{method:'POST',body:JSON.stringify({...item.input,expected:item.info,...reviewDecision(item.id)})});
  item.submitted=true;configuringRelay={relayUrl:item.input.url,connectionId:result.connectionId};render();
}

function dismissDialog(dialog){if(dialog.requestDismiss)void dialog.requestDismiss();else dialog.close();}
function makeDialog(id,title){
  let dialog=$(id);if(dialog){dialog.remove();}
  const opener=document.activeElement,key=opener?.closest('[data-key]')?.dataset.key,actionName=opener?.dataset.action;
  dialog=el('dialog',{id,'aria-label':title});const close=button('',()=>dismissDialog(dialog),'icon-button');close.setAttribute('aria-label','Close');close.append(icon('close'));
  dialog.append(el('div',{class:'dialog-heading'},el('h2',{},title),close));document.body.append(dialog);
  dialog.addEventListener('close',()=>requestAnimationFrame(()=>{
    if(document.querySelector('dialog[open]'))return;
    if(document.activeElement===document.body || !document.activeElement?.getClientRects().length){const target=opener?.getClientRects().length?opener:keyedControl(key,actionName)??keyedControl(key,'More options');(target?.getClientRects().length?target:$('main-content')).focus({preventScroll:true});}
  }));return dialog;
}
function openConnectionDetails(pair) {
  clearErrors();
  const dialog = makeDialog('connection-details-dialog',`Connection details · ${pair.name}`);
  dialog.append(el('div',{class:'dialog-body'},el('p',{class:'dialog-context'},pair.origin),
    el('div',{class:'job-details'},el('dl',{},...[
      ['App ID',pair.appId],['Account ID',pair.appUser],['Connected',new Date(pair.createdAt).toLocaleString()]
    ].flatMap(([label,value])=>[el('dt',{},label),el('dd',{},value)])))),
    el('div',{class:'dialog-actions'},button('Done',()=>dialog.close(),'primary')));
  dialog.showModal();
}
function grantSelect(value,choices){const select=el('select');for(const [key,title]of choices){const option=el('option',{value:key},title);select.append(option);}select.value=value;return {select,node:selectWrap(select)};}
function openPermissions(pair){openAppPermissions(pair,'folders');}
function renderFolderAccess(dialog,pair,folders,footer,changed,back,clearView,navigation){
  const paintFolders=(focusPath)=>{
    clearView();dialog.permissionView('Folder access','access',back);
    const body=el('div',{class:'dialog-body permission-editor'}),folderList=el('div',{class:'folder-grants'});
    for(const [index,folder]of folders.entries()){
      const access=grantSelect(folder.write?'write':'read',[['read','Read only'],['write','Read and edit']]);access.select.setAttribute('aria-label',`${folder.name} access`);
      access.select.dataset.permissionEdit='';
      const automatic=el('input',{type:'checkbox'});automatic.checked=folder.automatic;automatic.onchange=()=>{folder.automatic=automatic.checked;changed();};
      automatic.dataset.permissionEdit='';
      const policy=el('label',{class:'check-line folder-edit-policy'},automatic,el('span',{},'Allow edits without asking'));policy.hidden=!folder.write;
      access.select.onchange=()=>{folder.write=access.select.value==='write';policy.hidden=!folder.write;if(!folder.write){folder.automatic=false;automatic.checked=false;}changed();};
      const remove=el('button',{type:'button',class:'icon-button','aria-label':`Remove ${folder.name}`,title:'Remove folder',onclick:()=>{folders.splice(index,1);paintFolders();dialog.querySelector('[data-add-folder]').focus();}},icon('close'));
      remove.dataset.permissionEdit='';
      folderList.append(el('article',{class:'folder-grant','data-path':folder.path},el('div',{class:'folder-grant-heading'},icon('folder'),el('div',{},el('strong',{},folder.name),el('small',{},folder.path)),remove),el('label',{class:'folder-access-choice'},'Access',access.node),policy));
    }
    const add=el('button',{type:'button',class:'button secondary','data-add-folder':'',onclick:()=>chooseFolder(dialog,folders,(folder,parent)=>{folders.push({...folder,write:false,automatic:false});navigation.path=parent;paintFolders(folder.path);},path=>{navigation.path=path??navigation.path;paintFolders();dialog.querySelector('[data-add-folder]').focus();},navigation.path)},icon('plus'),'Add folder');add.disabled=folders.length>=16;
    add.dataset.permissionEdit='add-folder';
    if(!folders.length)folderList.append(el('div',{class:'folder-access-empty'},icon('folder'),el('h3',{},'No folders shared'),el('p',{},'Added folders start with read-only access.')));
    body.append(el('p',{class:'permission-draft-hint'},'Changes here are saved with the rest of this app’s permissions.'),folderList,add);dialog.append(body,footer);changed();
    if(focusPath)[...folderList.querySelectorAll('.folder-grant')].find(row=>row.dataset.path===focusPath)?.querySelector('select').focus();
  };
  paintFolders();
  if(!folders.length)dialog.querySelector('[data-add-folder]').focus();
}
async function chooseFolder(dialog,folders,selected,back,startPath=''){
  clearErrors();dialog.permissionView('Choose a folder','choose',()=>back(location?.path));
  for(const panel of dialog.querySelectorAll(':scope > .dialog-body, :scope > .dialog-actions, :scope > .permission-footer'))panel.remove();
  const body=el('div',{class:'dialog-body folder-picker'}),crumbs=el('nav',{class:'folder-breadcrumbs','aria-label':'Folder location'}),list=el('ul',{class:'folder-browser','aria-label':'Folders'});
  const search=el('input',{type:'search','aria-label':'Find a folder',placeholder:'Find a folder…',autocomplete:'off'});
  const path=el('input',{'aria-label':'Full folder path',placeholder:'Paste a folder path',autocomplete:'off',required:''});
  const go=el('button',{class:'button secondary',type:'submit'},'Go');
  const pathForm=el('form',{class:'folder-path-form',hidden:''},el('label',{},'Full folder path',path),go);
  const error=el('p',{class:'folder-error',role:'alert',hidden:''}),hint=el('p',{class:'folder-selection-hint',role:'status',hidden:''});
  const chosenName=el('strong'),chosenPath=el('small');
  const currentFolder=el('div',{class:'folder-selected'},icon('folder'),el('div',{},el('span',{class:'choice-label'},'Selected folder'),chosenName,chosenPath));
  const addLabel=el('span',{class:'folder-add-label'},'Add folder');
  const add=el('button',{type:'button',class:'button primary',disabled:'',onclick:()=>{if(!add.disabled&&location)selected({name:location.name,path:location.path},location.parent);}},icon('plus'),addLabel);
  let location,sequence=0,loading=false,valid=false;
  const up=el('button',{type:'button',class:'icon-button','aria-label':'Up one folder',title:'Up one folder',disabled:'',onclick:()=>browse(location.parent)},icon('arrow-up'));
  const homeButton=el('button',{type:'button',class:'icon-button','aria-label':'Home folder',title:'Home folder',onclick:()=>browse(location?.home??'')},icon('home'));
  const enterPath=el('button',{type:'button',class:'text-button','aria-expanded':'false',onclick:()=>{pathForm.hidden=!pathForm.hidden;enterPath.setAttribute('aria-expanded',String(!pathForm.hidden));enterPath.textContent=pathForm.hidden?'Enter a path':'Hide path';if(!pathForm.hidden){updateSelection();path.focus();path.select();}else void browse(location?.path??'');}},'Enter a path');
  const updateSelection=()=>{
    const edited=location&&path.value.trim()!==location.path,duplicate=location&&folders.some(folder=>folder.path===location.path);
    add.disabled=loading||!valid||!pathForm.hidden||edited||!location?.canShare||duplicate;
    currentFolder.hidden=loading||!valid||!pathForm.hidden||Boolean(edited);
    add.setAttribute('aria-label',location?`Add ${location.name}`:'Add folder');
    addLabel.textContent=location?`Add ${location.name}`:'Add folder';
    const message=loading?'Opening folder…':!error.hidden?'':!pathForm.hidden?'Choose Go to open this path.':!valid?'':duplicate?'This folder is already added.':!location.canShare?location.path===location.home?'Choose a folder inside Home.':'This location contains private app data. Choose another folder.':'';
    hint.textContent=message;hint.hidden=!message;
    go.disabled=loading||!path.value.trim();
  };
  const filter=()=>{
    if(!location||loading)return;
    const query=search.value.trim().toLocaleLowerCase(),matches=location.folders.filter(name=>name.toLocaleLowerCase().includes(query));
    list.replaceChildren(...matches.map(name=>el('li',{},el('button',{type:'button',class:'folder-entry','aria-label':`Open ${name}`,onclick:()=>browse(`${location.path}/${name}`)},icon('folder'),el('span',{},name),icon('chevron')))));
    if(!matches.length)list.append(el('li',{class:'folder-browser-empty',role:'status'},query?'No matching folders':'No subfolders'));
    if(location.truncated)list.append(el('li',{class:'folder-browser-note'},'Showing the first 500 folders. Enter a path to open another.'));
  };
  const browse=async(next='')=>{
    const request=++sequence;loading=true;valid=false;error.hidden=true;list.setAttribute('aria-busy','true');list.replaceChildren(el('li',{class:'folder-browser-empty'},icon('loader','spinner'),'Opening folder…'));
    up.disabled=true;homeButton.disabled=true;search.disabled=true;path.disabled=true;updateSelection();
    try{
      const result=await api('/folders',{method:'POST',body:JSON.stringify({path:next})});
      if(request!==sequence||!dialog.open||!body.isConnected)return;
      location=result;valid=true;path.value=result.path;search.value='';
      crumbs.replaceChildren(...result.breadcrumbs.map((crumb,index)=>el('span',{class:'folder-crumb'},...(index?[icon('chevron')]:[]),index===result.breadcrumbs.length-1?el('span',{'aria-current':'location'},crumb.name):el('button',{type:'button',onclick:()=>browse(crumb.path)},crumb.name))));
      chosenName.textContent=result.name;chosenPath.textContent=result.breadcrumbs[0]?.name==='Home'?result.breadcrumbs.map(crumb=>crumb.name).join(' / '):result.path;chosenPath.title=result.path;
      pathForm.hidden=true;enterPath.setAttribute('aria-expanded','false');enterPath.textContent='Enter a path';
    }catch(failure){if(request===sequence&&dialog.open&&body.isConnected){error.textContent=failure instanceof Error?failure.message:'Folder could not be opened.';error.hidden=false;list.replaceChildren(el('li',{class:'folder-browser-empty'},'Choose another location.'));}}
    finally{if(request===sequence&&dialog.open&&body.isConnected){loading=false;list.removeAttribute('aria-busy');up.disabled=!location||location.parent===location.path;homeButton.disabled=false;search.disabled=!valid;path.disabled=false;updateSelection();if(valid){filter();search.focus({preventScroll:true});}else if(!pathForm.hidden)path.focus({preventScroll:true});}}
  };
  pathForm.onsubmit=event=>{event.preventDefault();if(!go.disabled)void browse(path.value.trim());};path.oninput=updateSelection;search.oninput=filter;
  body.append(el('div',{class:'folder-toolbar'},up,homeButton,crumbs),enterPath,pathForm,error,el('div',{class:'folder-search'},icon('search'),search),list,currentFolder,hint);
  dialog.append(body,el('div',{class:'dialog-actions'},el('span',{class:'folder-draft-note'},'Access is granted when you save.'),el('button',{type:'button',class:'button secondary',onclick:()=>back(location?.path)},'Back'),add));
  await browse(startPath);
}
function renderPermissionRequests(){
  const permissions=state.permissions??[];$('permission-section').hidden=!permissions.length;
  preserveList($('permission-list'),permissions.map(request=>{
    const pair=state.pairings.find(item=>item.id===request.pairingId),title=request.kind==='file-write'?`Edit ${request.name}`:request.kind==='files'?'Send and receive files':'Folder access requested';
    const card=el('article',{class:'job-card approval-card','data-key':request.id},el('div',{class:'job-header'},el('div',{},el('strong',{},title),el('small',{class:'job-meta'},pair?.name??'App')),icon('shield')));
    if(request.kind==='files')card.append(el('p',{},'Allow this app to supply images and documents for its requests and receive files those requests produce. This permission does not grant access to your computer’s folders.'),el('small',{},'Saved for this connection. You can turn it off in Permissions.'));
    if(request.kind==='file-write')card.append(el('details',{},disclosure('Review changes'),el('div',{class:'write-preview'},el('div',{},el('h4',{},'Current'),el('pre',{},request.before??'')),el('div',{},el('h4',{},'Proposed'),el('pre',{},request.after??'')))));
    const decide=approve=>api(`/permissions/${request.id}/decision`,{method:'POST',body:JSON.stringify({approve})});
    card.append(el('div',{class:'actions'},request.kind==='access'?button('Choose folders',()=>openPermissions(pair),'primary'):button(request.kind==='files'?'Allow files':'Approve',()=>decide(true),'primary'),button('Decline',()=>decide(false))));return card;
  }));
}
