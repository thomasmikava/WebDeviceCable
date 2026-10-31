import { BridgeClient } from './index.js';
const $ = id => document.getElementById(id);
let securityNoticeShown = false;
let csrf = '', controlSession = '', state, demo, demoPairID, invitation, lastState = '';
const controlStorageKey='wdc-control-session';
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
  const key = focusControl?.closest('[data-key]')?.dataset.key, actionName = focusControl?.dataset.action;
  target?.setAttribute('aria-busy', 'true');
  if (control) { control.disabled = true; control.prepend(spinner); }
  try { clearErrors(); await fn(event); await refresh(); } catch (error) { notify(error); }
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
  if (!response.ok) { const data=await response.json(); if (response.status === 401) lock(); throw new Error(data.error?.message ?? 'Can’t reach the local runner.'); }
  return response;
}
async function api(path, init = {}) { return (await controlFetch(path,init)).json(); }
async function saveArtifact(job, artifact) {
  const response=await controlFetch(`/jobs/${job.id}/artifacts/${artifact.id}`),url=URL.createObjectURL(await response.blob());
  const link=el('a',{href:url,download:artifact.name}); document.body.append(link);link.click();link.remove();
  setTimeout(()=>URL.revokeObjectURL(url),60000);
}
function lock() { forgetControlSession(); csrf = ''; for (const dialog of document.querySelectorAll('dialog[open]')) dialog.close(); document.querySelector('.skip-link').hidden = true; $('shell').hidden = true; $('locked').hidden = false; }
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
function confirmAction(title, description, label, destructive = false) {
  const dialog = $('confirm-dialog'); $('confirm-title').textContent = title; $('confirm-description').textContent = description; $('accept-confirm').textContent = label;
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
  if (event.target === backdropDialog && backdropDialog?.open && outsideDialog(event,backdropDialog)) backdropDialog.close();
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
  return el('span',{class:`provider-mark${small ? ' small' : ''}`,'aria-hidden':'true'},el('img',{src:provider === 'claude' ? '/claude-icon.png' : '/chatgpt-icon.png',alt:'',width:'24',height:'24'}));
}
function nativeLabel(native) { return native?.execution ? 'Ready' : state.nativeChecking ? 'Checking…' : native?.installed ? 'Unavailable' : native ? 'Not installed' : 'Not checked'; }
function clientRow(provider, native) {
  let hint = native ? native.installed ? native.version ? `Installed · ${native.version}` : 'Installed' : 'Not installed' : state.nativeChecking ? 'Checking installed client…' : 'Not checked';
  if (native?.authentication === 'native_subscription') hint = 'Signed in with your subscription';
  if (native?.authentication === 'not_signed_in_or_unavailable' && provider.id === 'codex') hint = 'Sign in using the installed client';
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
  if(target?.provider && (state.providersByPairing?.[pair.id] ?? state.providers).some(provider=>provider.id===target.provider && !provider.execution)) choice.append(el('span',{class:'choice-note attention'},'Unavailable for this app'));
  if(target?.action && !state.commands.some(command=>command.id===target.action && command.hash===pair.actions?.[command.id])) choice.append(el('span',{class:'choice-note attention'},'Review command access'));
  const menu = el('details',{class:'app-menu'},el('summary',{class:'icon-button','aria-label':`More options for ${pair.name}`,'data-action':'More options',title:'More options'},icon('more')));
  const menuAction = (label, fn, extra='') => button(label,async () => {menu.open=false;await fn();},extra);
  const items = el('div',{class:'action-menu'},
    menuAction('Program settings',() => openRouting(pair)),
    menuAction('Connection details',() => openConnectionDetails(pair)),
    menuAction(pair.paused ? 'Resume requests' : 'Pause requests',() => api(`/pairings/${pair.id}`,{method:'PATCH',body:JSON.stringify({paused:!pair.paused})})),
    state.commands.length || grants ? menuAction('Command access',() => openCommandAccess(pair)) : null,
    menuAction('Folder access',() => openPermissions(pair)),
    menuAction('Disconnect',async () => {if(await confirmAction(`Disconnect ${pair.name}?`,'Its active requests will be cancelled. Connecting again requires a new invitation.','Disconnect',true))await api(`/pairings/${pair.id}`,{method:'PATCH',body:JSON.stringify({revoke:true})});},'danger'));
  menu.append(items);
  menu.addEventListener('toggle',() => {if(menu.open && menu.isConnected)for(const other of document.querySelectorAll('.app-menu[open]'))if(other!==menu)other.open=false;});
  const approval = el('select',{'aria-label':`Request approval for ${pair.name}`,'data-action':'Request approval'},el('option',{value:'ask'},'Ask each time'),el('option',{value:'automatic'},'Run automatically'));
  approval.value = pair.automatic ? 'automatic' : 'ask';
  approval.onchange = action(async () => {
    const automatic = approval.value === 'automatic';
    approval.value = pair.automatic ? 'automatic' : 'ask'; approval.disabled = true;
    try {
      if (automatic && !await confirmAction('Run requests automatically?',`${pair.name} can run requests within its allowed access without asking each time.`,'Allow automatically')) return;
      await api(`/pairings/${pair.id}`,{method:'PATCH',body:JSON.stringify({automatic})});
    } finally { approval.disabled = false; }
  });
  const access = el('div',{class:'connection-meta'});
  if (folderCount) access.append(button(`${folderCount} shared folder${folderCount===1?'':'s'}`,() => openPermissions(pair),'text-button'));
  if (grants) access.append(button(grants!==granted ? 'Review command access' : `${granted} allowed command${granted===1?'':'s'}`,() => openCommandAccess(pair),'text-button'));
  return el('article',{class:`app-connection${pair.paused?' paused':''}`,'data-key':pair.id,tabindex:'-1'},
    el('div',{class:'connection-header'},
      el('div',{class:'connection-identity'},el('span',{class:'app-mark','aria-hidden':'true'},icon('app')),el('div',{class:'app-title'},el('strong',{},pair.name),el('small',{},pair.origin))),
      el('div',{class:'connection-controls'},el(pending?'a':'span',{class:`connection-status${pending?' attention':''}${working?' working':''}`,...(pending?{href:'#activity'}:{})},status),menu)),
    el('div',{class:'connection-settings'},choice,el('label',{class:'connection-approval'},el('span',{class:'choice-label'},'Request approval'),selectWrap(approval))),
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
  if (job.inputNames?.length) content.append(el('p',{class:'job-detail'},icon('document'),job.inputNames.join(' · ')));
  for (const artifact of job.artifacts ?? []) { const save=button(artifact.mediaType.startsWith('image/')?'Save image':'Save file',()=>saveArtifact(job,artifact));save.prepend(icon('download'));content.append(save); }
  if(job.status==='failed')content.append(el('p',{class:'job-detail'},job.error?.message || job.detail));
  const details = [];
  const definition = command && state.commands.find(item=>item.id===job.plan?.action && item.hash===job.plan?.actionHash);
  if(definition) details.push(['Program',definition.exec[0]]);
  if (job.execution?.name) details.push(['Executed',job.execution.name]);
  if (job.execution?.model) details.push(['Model',job.execution.model]);
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
  renderPermissionRequests();
  renderConnectionRequests();
  const apps = state.pairings.filter(p => !p.revoked).sort((a,b) => a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id));
  const approvals = state.jobs.filter(j => j.status === 'awaiting_approval');
  const pendingCount=approvals.length+(state.permissions?.length??0);$('approval-count').textContent=pendingCount;$('approval-count').hidden=!pendingCount;
  $('runner-state').textContent = state.enabled ? 'Device ready' : 'Requests paused'; $('runner-state').dataset.status = state.enabled ? 'ready' : 'paused'; $('toggle-runner').setAttribute('aria-checked',String(state.enabled));
  $('app-list-count').textContent = apps.length || ''; $('apps-section-heading').hidden = !apps.length || !state.pairingRequests?.length;
  $('apps-empty').hidden = !!apps.length || !!state.pairingRequests?.length; $('connect-app').hidden = !apps.length;
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
  const descriptions = {
    codex: state.providers.find(p => p.id === 'codex')?.reason,
    claude: state.providers.find(p => p.id === 'claude')?.reason
  };
  const openProviders = new Set([...$('provider-list').querySelectorAll('details[open]')].map(node => node.dataset.provider));
  preserveList($('provider-list'), state.providers.map(p => el('article', {class:'provider-item','data-key':p.id,tabindex:'-1'}, clientRow(p, state.native?.find(n => n.id === p.id)), el('details', {class:'provider-details','data-provider':p.id,...(openProviders.has(p.id) ? {open:''} : {})}, disclosure(p.execution ? 'Connection details' : 'Why unavailable?'), el('div', {class:'detail-body'}, el('p', {}, descriptions[p.id]), el('a', {href:p.source,target:'_blank',rel:'noreferrer','data-action':'documentation'}, 'Official documentation', icon('external')))))));
  $('check-clients').textContent = state.nativeChecking ? 'Checking…' : 'Check again'; $('check-clients').disabled = state.nativeChecking;
}
async function refresh() {
  if (!csrf) return;
  const next = await api('/state'); next.pairings.sort((a,b) => a.id.localeCompare(b.id));
  if (next.securityReset && !securityNoticeShown) { securityNoticeShown = true; notify('Security updated. Reconnect your apps.'); }
  const serialized = JSON.stringify(next); state = next;
  if ($('runner-state').dataset.status === 'offline') { $('runner-state').textContent = next.enabled ? 'Device ready' : 'Requests paused'; $('runner-state').dataset.status = next.enabled ? 'ready' : 'paused'; }
  if (serialized !== lastState) { lastState = serialized; render(); }
}
function openConnect() {
  clearErrors(); $('invite-manual-copy')?.remove(); $('invite-copy-hint').textContent='Paste it into your app. Expires in 5 minutes.';
  $('invite-form').reset(); for(const detail of $('invite-form').querySelectorAll('details'))detail.open=false;
  $('connect-start').hidden=false; $('invite-form').hidden=true; $('invite-result').hidden=true; $('connect-title').textContent='Connect an app';
  $('invite-commands-section').hidden=!state.commands.length; commandPicker($('invite-commands'),{},$('invite-command-consent')); $('connect-dialog').showModal();
}
$('connect-app').onclick=openConnect; $('connect-first').onclick=openConnect;
let reviewedRelay;
$('connect-server').onclick=()=>{reviewedRelay=undefined;$('relay-form').reset();$('relay-review').textContent='';$('relay-submit').textContent='Review connection';$('relay-dialog').showModal();};
$('close-relay').onclick=()=>$('relay-dialog').close();
$('relay-form').oninput=()=>{reviewedRelay=undefined;$('relay-submit').textContent='Review connection';$('relay-review').textContent='';};
$('relay-form').onsubmit=action(async event=>{
  event.preventDefault();const data=new FormData(event.currentTarget);const input={url:String(data.get('url')).replace(/\/$/,''),code:String(data.get('code')).trim()};
  if(!reviewedRelay){reviewedRelay=await api('/relay/inspect',{method:'POST',body:JSON.stringify(input)});$('relay-review').textContent=reviewedRelay.name+' · '+reviewedRelay.origin+' · Account '+reviewedRelay.appUser+'. Approve this server to request work from this device?';$('relay-submit').textContent='Approve server connection';return;}
  await api('/relay/connect',{method:'POST',body:JSON.stringify({...input,expected:reviewedRelay})});$('relay-dialog').close();await refresh();
});
$('show-invitation-form').onclick=()=>{$('connect-start').hidden=true;$('invite-form').hidden=false;$('connect-title').textContent='Create an invitation';$('invite-form').elements.name.focus();};
$('back-connect').onclick=()=>{$('invite-form').hidden=true;$('connect-start').hidden=false;$('connect-title').textContent='Connect an app';$('show-invitation-form').focus();};
$('close-connect').onclick=$('done-connect').onclick=$('done-connect-start').onclick=()=>$('connect-dialog').close();
$('dismiss-notice').onclick = () => { $('notice').hidden = true; };
$('invite-form').onsubmit = action(async event => {
  event.preventDefault(); const data = new FormData(event.target), origin = new URL(String(data.get('origin')).trim()).origin;
  const appId = String(data.get('appId')).trim() || new URL(origin).hostname;
  const grants = selectedCommands($('invite-commands'));
  const result = await api('/invitations', {method:'POST',body:JSON.stringify({name:data.get('name'),appId,origin,appUser:data.get('appUser'),automatic:data.get('automatic') === 'true',actions:grants,commandPermissionsAccepted:data.get('commandConsent') === 'on'})});
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
  $('build-version').textContent=update?.official?`WebDeviceCable ${update.version}`:update?.version&&update.version!=='dev'?`${update.version} preview`:'Development build';
  const messages={development:'Updates are available in official installations.',application:'Open the WebDeviceCable app to install updates.',idle:'Checks daily. You can check anytime.',checking:'Checking for updates…',current:'WebDeviceCable is up to date.',available:`WebDeviceCable ${update?.available} is available.`,downloading:'Downloading update…',ready:'Update ready to install.',waiting:'Update ready. Waiting for sessions to finish.',authorization:'Update ready. macOS needs your permission to install.',installing:'Updating WebDeviceCable…',manual:'This update needs a manual installation.',error:update?.error??'Couldn’t check for updates. Try again.'};
  $('update-status').textContent=messages[update?.stage]??messages.development;
  $('check-updates').disabled=!supported||busy;
  $('manual-update').hidden=update?.stage!=='manual';
  $('install-update').hidden=!supported||!['available','ready','waiting','authorization'].includes(update?.stage);$('install-update').textContent=update?.stage==='waiting'?'End sessions and update':'Update now';
  $('update-progress').hidden=update?.stage!=='downloading';
  if(update?.size&&update?.downloaded){$('update-progress').max=update.size;$('update-progress').value=update.downloaded;}else{$('update-progress').removeAttribute('value');}
}
$('run-test').onclick = action(async () => {
  if (demo && !state.pairings.some(p => p.id === demoPairID && !p.revoked && !p.paused)) { await demo.close().catch(() => {}); demo = null; }
  if (!demo) { const origin = location.origin; const result = await api('/invitations', {method:'POST',body:JSON.stringify({name:'Connection test',appId:'webdevicecable.local-test',origin,appUser:'This computer',mode:'restricted',automatic:false})}); const paired = await BridgeClient.pair({baseURL:origin,invitation:result.invitation,appId:'webdevicecable.local-test',appUser:'This computer',origin}); demoPairID = paired.pairing.id; demo = new BridgeClient({baseURL:origin,connection:paired.connection,origin}); }
  await demo.connect(); await demo.submit({idempotencyKey:crypto.randomUUID(),kind:'connection-test'}); location.hash = 'activity';
});
let editingCommand, accessPairID, routingPairID, routingRevision, routingReturn;
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
let refreshTimer,booting=false;
document.addEventListener('invalid', event => { let parent = event.target.parentElement; while (parent) { if (parent.tagName === 'DETAILS') parent.open = true; parent = parent.parentElement; } }, true);
async function boot() {
  if(booting)return;booting=true;clearInterval(refreshTimer);
  try {
    const key = new URLSearchParams(location.hash.slice(1)).get('key');
    let session;
    if (key) { history.replaceState(null, '', '/'); session=await api('/session', {method:'POST',headers:{Authorization:`Bearer ${key}`}}); }
    else session=await api('/session');
    csrf=session.csrf; controlSession=session.session; storeControlSession();
    $('locked').hidden = true; $('shell').hidden = false; document.querySelector('.skip-link').hidden = false; route(); await refresh(); initPairingRequest();
    refreshTimer=setInterval(() => { void refresh().catch(() => { $('runner-state').textContent = 'Runner offline'; $('runner-state').dataset.status = 'offline'; }); }, 1500);
  } catch { lock(); } finally {booting=false;}
}
void boot();

function targetLabel(target) { return target?.action ? state.commands.find(command => command.id === target.action)?.title ?? 'Unavailable command' : target?.provider === 'codex' ? 'Codex' : target?.provider === 'claude' ? 'Claude Code' : 'Use default'; }
function routeEditor(pair, group, current) {
  const providers=state.providersByPairing?.[pair.id] ?? state.providers;
  const selector = el('select',{'data-field':'target'},el('option',{value:''},group ? (group.parentId ? `Use ${pair.groups.find(parent => parent.id === group.parentId)?.name ?? 'parent'} setting` : 'Use default program') : 'Choose a program'));
  if (!group || group.kind === 'ai') for(const provider of providers) selector.append(el('option',{value:`provider:${provider.id}`},provider.name + (provider.execution ? '' : ' (unavailable)')));
  for (const command of state.commands) if (pair.actions?.[command.id] === command.hash) selector.append(el('option',{value:`command:${command.id}`},command.title));
  const value = current?.action ? `command:${current.action}` : current?.provider && (!group || group.kind === 'ai') ? `provider:${current.provider}` : '';
  if (value && ![...selector.options].some(option => option.value === value)) selector.append(el('option',{value},`${targetLabel(current)} (review access)`)); selector.value = value;
  const model = el('select',{'data-field':'model'},el('option',{value:''},'Client default'));
  const reasoning = el('select',{'data-field':'reasoning'},el('option',{value:''},'Client default'));
  let selectedProvider;
  const modelOptions = () => {
    const provider=selector.value.replace('provider:',''),changed=selectedProvider!==undefined&&selectedProvider!==provider;
    const models=state.native?.find(native=>native.id===provider)?.models ?? [];
    const value=selectedProvider === undefined ? current?.model ?? '' : changed ? '' : model.value;
    model.replaceChildren(el('option',{value:''},'Client default'),...models.map(item=>el('option',{value:item.model},item.displayName || item.model)));
    if(value&&![...model.options].some(option=>option.value===value))model.append(el('option',{value},value));model.value=value;
    const efforts=models.find(item=>item.model===value)?.supportedReasoningEfforts?.map(item=>item.reasoningEffort) ?? ['low','medium','high','xhigh'];
    const effort=selectedProvider === undefined ? current?.reasoning ?? '' : changed ? '' : reasoning.value;
    reasoning.replaceChildren(el('option',{value:''},'Client default'),...efforts.map(value=>el('option',{value},({low:'Low',medium:'Medium',high:'High',xhigh:'Extra high',max:'Maximum',none:'None'})[value] ?? value)));
    if(effort&&![...reasoning.options].some(option=>option.value===effort))reasoning.append(el('option',{value:effort},effort));reasoning.value=effort;selectedProvider=provider;
  };
  model.onchange=modelOptions;
  const availability=el('p',{class:'route-note',hidden:''});
  const native = el('div',{'data-native-fields':''},availability,el('details',{class:'advanced'},disclosure('Model & reasoning'),el('label',{},'Model',selectWrap(model)),el('label',{},'Reasoning',selectWrap(reasoning))));
  const sourceLabel=el('small',{class:'route-selection'});
  const id=`route-${pair.id}-${group?.id ?? 'default'}`;selector.id=id;
  const update=()=>{modelOptions();const provider=providers.find(item=>`provider:${item.id}`===selector.value);availability.hidden=!provider || provider.execution;availability.textContent=provider?.reason ?? '';native.hidden=!selector.value.startsWith('provider:');};
  selector.onchange=()=>{update();refreshRoutingLabels(pair);};update();
  // Native model options remain an explicit, optional per-selection setting.
  const label=el('label',{class:'route-label',for:id},el('span',{},group?.name ?? 'Default program'),
    group && !(group.kind==='ai' && group.name.toLowerCase()==='ai') ? el('span',{class:'group-kind'},group.kind==='ai'?'AI':'Commands') : null,sourceLabel);
  return el('div',{class:`route-editor${group?'':' default-route'}`,'data-group':group?.id ?? ''},el('div',{class:'route-main'},label,selectWrap(selector)),native);
}
function refreshRoutingLabels(pair) {
  const rows=[...$('routing-groups').querySelectorAll('[data-group]')], defaultRow=$('routing-default').firstElementChild;
  for(const row of rows){
    const group=pair.groups.find(item=>item.id===row.dataset.group), selector=row.querySelector('[data-field=target]');
    const parentId=group?.parentId;
    let source=rows.find(item=>item.dataset.group===parentId);
    while(source && !source.querySelector('[data-field=target]').value){const ancestor=pair.groups.find(item=>item.id===source.dataset.group)?.parentId;source=rows.find(item=>item.dataset.group===ancestor);}
    const selected=(source??defaultRow)?.querySelector('[data-field=target]');
    const incompatible=group?.kind!=='ai' && selected?.value.startsWith('provider:');
    const inherited=!selector.value;
    const effective=selected?.value ? selected.selectedOptions[0].textContent.replace(' (unavailable)','') : '';
    selector.options[0].textContent=incompatible ? 'Choose a command' : effective ? `Default · ${effective}` : 'Use default';
    const hint=row.querySelector('.route-selection');
    hint.textContent=inherited ? incompatible ? 'Choose a command for this group' : !selected?.value ? 'Choose a default above' : `Uses ${effective}` : 'Custom choice';
    hint.classList.toggle('needs-choice',incompatible && inherited);
  }
  if(defaultRow)defaultRow.querySelector('.route-selection').textContent='Used unless a group has its own choice';
}
function openRouting(pair) {
  $('routing-command-access').hidden=!state.commands.length && !Object.keys(pair.actions??{}).length;
  clearErrors(); routingPairID = pair.id; routingRevision = pair.routing?.revision ?? 0;
  $('routing-title').textContent = `Program settings · ${pair.name}`;
  $('routing-context').textContent = pair.origin;
  $('routing-default').replaceChildren(routeEditor(pair,null,pair.routing?.default));
  const rows = [];
  const append = (parent = '', depth = 0) => {
    for (const group of pair.groups ?? []) if ((group.parentId ?? '') === parent) {
      const row = routeEditor(pair,group,pair.routing?.groups?.[group.id]);
      row.style.setProperty('--group-depth',String(Math.min(depth,3)));
      if (depth) row.classList.add('subgroup');
      rows.push(row); append(group.id,depth + 1);
    }
  };
  append();
  $('routing-groups').replaceChildren(...(rows.length ? [el('div',{class:'route-heading'},el('h3',{},'Choose by task')),...rows] : []));
  refreshRoutingLabels(pair);
  $('routing-dialog').showModal();
}
$('close-routing').onclick = () => $('routing-dialog').close();
function readRoutingForm() {
  const read=row=>{const value=row.querySelector('[data-field=target]').value;if(!value)return null;const [kind,id]=value.split(':');return kind==='command'?{action:id}:{provider:id,model:row.querySelector('[data-field=model]').value.trim(),reasoning:row.querySelector('[data-field=reasoning]').value.trim()};};
  const routing={default:read($('routing-default').firstElementChild),groups:{}};
  for(const row of $('routing-groups').querySelectorAll('[data-group]')){const target=read(row);if(target)routing.groups[row.dataset.group]=target;}
  return routing;
}
$('routing-command-access').onclick=()=>{
  routingReturn={pairID:routingPairID,revision:routingRevision,draft:readRoutingForm()};
  $('routing-dialog').close();openCommandAccess(state.pairings.find(pair=>pair.id===routingPairID));
};
$('command-access-dialog').addEventListener('close',()=>{
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
  const announce = () => { if (!incomingPairing) opener.postMessage({type:'wdc:pairing-ready',requestId},'*'); };
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
    const {appId,name,appUser} = event.data;
    if (![appId,name,appUser].every(value => typeof value === 'string' && value.trim()) || appId.length > 100 || name.length > 100 || appUser.length > 200) return;
    incomingPairing = {requestId,opener,origin:event.origin,appId,name,appUser};clearInterval(timer);
    try {await api('/pairing-requests',{method:'POST',body:JSON.stringify({id:requestId,origin:event.origin,appId,name,appUser})});registered=true;await refresh();}
    catch(error){notify(error);stop();}
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
function renderConnectionRequests() {
  const requests = state.pairingRequests ?? [];
  $('connection-requests').hidden = !requests.length;
  $('connection-request-count').textContent = requests.length; $('connection-request-count').hidden = !requests.length;
  document.title = requests.length ? `(${requests.length}) WebDeviceCable` : 'WebDeviceCable';
  preserveList($('connection-request-list'),requests.map(request => el('article',{class:'connection-request','data-key':request.id},
    el('div',{class:'connection-request-heading'},icon('external'),el('strong',{},request.name)),
    el('p',{class:'connection-request-origin'},request.origin),
    el('details',{class:'job-details'},disclosure('Account details'),el('dl',{},el('dt',{},'Account ID'),el('dd',{},request.appUser),el('dt',{},'App ID'),el('dd',{},request.appId))),
    el('div',{class:'connection-request-actions'},el('small',{},'Asks before running requests'),el('div',{class:'actions'},button('Decline',()=>api(`/pairing-requests/${request.id}/decision`,{method:'POST',body:JSON.stringify({approve:false})})),button('Connect',()=>api(`/pairing-requests/${request.id}/decision`,{method:'POST',body:JSON.stringify({approve:true})}),'primary'))))));
}

// Permissions stay out of the main connection layout until the user needs them.
function makeDialog(id,title){
  let dialog=$(id);if(dialog){dialog.remove();}
  const opener=document.activeElement,key=opener?.closest('[data-key]')?.dataset.key,actionName=opener?.dataset.action;
  dialog=el('dialog',{id,'aria-label':title});const close=button('',()=>dialog.close(),'icon-button');close.setAttribute('aria-label','Close');close.append(icon('close'));
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
function openPermissions(pair){
  clearErrors();const dialog=makeDialog('permissions-dialog',`Folder access · ${pair.name}`),folders=(pair.folders??[]).map(f=>({...f}));let browseFrom='';
  const selection=()=>JSON.stringify(folders.map(({id,path,write,automatic})=>({id,path,write:Boolean(write),automatic:Boolean(automatic)}))),initial=selection();
  const paintFolders=(focusPath)=>{
    clearErrors();dialog.dataset.view='access';dialog.setAttribute('aria-label',`Folder access · ${pair.name}`);dialog.querySelector('h2').textContent=`Folder access · ${pair.name}`;
    for(const panel of dialog.querySelectorAll(':scope > .dialog-body, :scope > .dialog-actions'))panel.remove();
    const body=el('div',{class:'dialog-body permission-editor'}),folderList=el('div',{class:'folder-grants'});
    const dirty=selection()!==initial,unsaved=el('span',{class:'folder-unsaved',role:'status',...(dirty?{}:{hidden:''})},'Unsaved changes');
    const save=button('Save access',async()=>{await api(`/pairings/${pair.id}/permissions`,{method:'PUT',body:JSON.stringify({folders})});dialog.close();},'primary');save.disabled=!dirty;
    const changed=()=>{const modified=selection()!==initial;save.disabled=!modified;unsaved.hidden=!modified;};
    for(const [index,folder]of folders.entries()){
      const access=grantSelect(folder.write?'write':'read',[['read','Read only'],['write','Read and edit']]);access.select.setAttribute('aria-label',`${folder.name} access`);
      const automatic=el('input',{type:'checkbox'});automatic.checked=folder.automatic;automatic.onchange=()=>{folder.automatic=automatic.checked;changed();};
      const policy=el('label',{class:'check-line folder-edit-policy'},automatic,el('span',{},'Allow edits without asking'));policy.hidden=!folder.write;
      access.select.onchange=()=>{folder.write=access.select.value==='write';policy.hidden=!folder.write;if(!folder.write){folder.automatic=false;automatic.checked=false;}changed();};
      const remove=el('button',{type:'button',class:'icon-button','aria-label':`Remove ${folder.name}`,title:'Remove folder',onclick:()=>{folders.splice(index,1);paintFolders();dialog.querySelector('[data-add-folder]').focus();}},icon('close'));
      folderList.append(el('article',{class:'folder-grant','data-path':folder.path},el('div',{class:'folder-grant-heading'},icon('folder'),el('div',{},el('strong',{},folder.name),el('small',{},folder.path)),remove),el('label',{class:'folder-access-choice'},'Access',access.node),policy));
    }
    const add=el('button',{type:'button',class:'button secondary','data-add-folder':'',onclick:()=>chooseFolder(dialog,folders,(folder,parent)=>{folders.push({...folder,write:false,automatic:false});browseFrom=parent;paintFolders(folder.path);},path=>{browseFrom=path??browseFrom;paintFolders();dialog.querySelector('[data-add-folder]').focus();},browseFrom)},icon('plus'),'Add folder');add.disabled=folders.length>=16;
    if(!folders.length)folderList.append(el('div',{class:'folder-access-empty'},icon('folder'),el('h3',{},'No folders shared'),el('p',{},'Added folders start with read-only access.')));
    body.append(folderList,add);dialog.append(body,el('div',{class:'dialog-actions'},unsaved,button('Cancel',()=>dialog.close()),save));
    if(focusPath)[...folderList.querySelectorAll('.folder-grant')].find(row=>row.dataset.path===focusPath)?.querySelector('select').focus();
  };
  paintFolders();dialog.showModal();
  if(!folders.length)dialog.querySelector('[data-add-folder]').focus();
}
async function chooseFolder(dialog,folders,selected,back,startPath=''){
  clearErrors();dialog.dataset.view='choose';dialog.setAttribute('aria-label','Choose a folder');dialog.querySelector('h2').textContent='Choose a folder';
  for(const panel of dialog.querySelectorAll(':scope > .dialog-body, :scope > .dialog-actions'))panel.remove();
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
    const pair=state.pairings.find(item=>item.id===request.pairingId),title=request.kind==='file-write'?`Edit ${request.name}`:'Folder access requested';
    const card=el('article',{class:'job-card approval-card','data-key':request.id},el('div',{class:'job-header'},el('div',{},el('strong',{},title),el('small',{class:'job-meta'},pair?.name??'App')),icon('shield')));
    if(request.kind==='file-write')card.append(el('details',{},disclosure('Review changes'),el('div',{class:'write-preview'},el('div',{},el('h4',{},'Current'),el('pre',{},request.before??'')),el('div',{},el('h4',{},'Proposed'),el('pre',{},request.after??'')))));
    const decide=approve=>api(`/permissions/${request.id}/decision`,{method:'POST',body:JSON.stringify({approve})});
    card.append(el('div',{class:'actions'},request.kind==='access'?button('Choose folders',()=>openPermissions(pair),'primary'):button('Approve',()=>decide(true),'primary'),button('Decline',()=>decide(false))));return card;
  }));
}
