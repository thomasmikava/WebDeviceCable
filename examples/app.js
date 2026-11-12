import { BridgeClient, ConnectionStore } from './sdk.js';
import {exampleEndpoint, assertExampleConnection} from './isolation.js';
const $ = id => document.getElementById(id); let client, current, chat, chatProvider, commands = [];
const origin = location.origin;
const baseURL = exampleEndpoint(document.querySelector('meta[name=bridge-url]')?.content);
const account = {appId:'writer.example',appUser:'alice'};
const store = new ConnectionStore(account);
let saved, busy = false;
function errorMessage(error) { $('error').textContent = error.message; }
function setBusy(value) {
  busy=value;
  for(const button of document.querySelectorAll('#connected button')) button.disabled=value;
  $('cancel').disabled=!value;
}
// Restore the pairing only. Each tab opens its own lease and chat context.
$('connect').disabled=true;
void store.load().then(connection => { if(connection)assertExampleConnection(connection,baseURL);saved=connection; if(saved) $('connect').textContent='Reconnect to WebDeviceCable'; }).catch(errorMessage).finally(()=>{$('connect').hidden=!saved;$('connect').disabled=false;});
async function submit(request) {
  if(busy) return; setBusy(true); $('error').textContent = '';
  try {
    if(request.kind==='text'){
      if(!chat || chatProvider!==(request.provider || 'default')){await chat?.close();chat=client.session(request.provider ? {provider:request.provider} : {group:'ai.chat'});chatProvider=request.provider || 'default';}
      current=await chat.send(request.prompt);
    }else current = await client.submit({idempotencyKey:crypto.randomUUID(),...request}); $('cancel').disabled = false;
    for await (const job of client.stream(current.id)) $('output').textContent = `${job.status}\n${job.output || job.detail || (job.status==='awaiting_approval'?'Approve the request in WebDeviceCable.':job.status==='queued'?'Waiting to start.':'Working…')}`;
  } catch (error) { $('error').textContent = error.message; }
  finally { setBusy(false); }
}
$('pair').onsubmit = async event => {
  event.preventDefault();
  try {
    const result = await BridgeClient.pair({baseURL,invitation:$('invitation').value.trim(),appId:'writer.example',appUser:'alice',origin});
    await connected(result);
  } catch(error) { $('error').textContent = error.message; }
};
$('test').onclick = () => submit({kind:'connection-test'});
$('request').onsubmit = event => { event.preventDefault(); void submit({kind:'text',provider:$('provider').value,prompt:$('prompt').value}); };
$('cancel').onclick = async () => { try { await client.cancel(current.id); } catch(error) { $('error').textContent = error.message; } };
function renderInputs() {
  const command = commands.find(command => command.id === $('command-select').value);
  $('command-inputs').replaceChildren(...(command?.arguments ?? []).map(argument => {
    const label = document.createElement('label'); label.append(document.createTextNode(argument.title || argument.name));
    const input = document.createElement(argument.choices?.length ? 'select' : argument.type === 'raw_string_multiline' ? 'textarea' : 'input');
    if (argument.choices?.length) input.replaceChildren(...argument.choices.map(choice => { const option = document.createElement('option'); option.value = choice.value; option.textContent = choice.title || choice.value; return option; }));
    input.name = argument.name; input.required = !!argument.rejectNull; input.value = argument.default ?? ''; label.append(input); return label;
  }));
}
$('command-select').onchange = renderInputs;
$('command-request').onsubmit = event => { event.preventDefault(); void submit({kind:'command',action:$('command-select').value,arguments:Object.fromEntries(new FormData(event.target))}); };

async function connected(result) {
  assertExampleConnection(result.connection,baseURL);
  if(client) await client.close();
  client = new BridgeClient({baseURL,connection:result.connection,account,origin,groups:[{id:'ai',name:'AI',kind:'ai',children:[{id:'chat',name:'Chat',kind:'ai'},{id:'courses',name:'Course creation',kind:'ai'}]},{id:'export',name:'Export'}]});
  await client.connect();
  saved=result.connection;
  await store.save(saved).catch(errorMessage);
  const providers=(await client.capabilities()).providers;
  $('provider').replaceChildren(new Option('Use my WebDeviceCable choice',''),...providers.map(provider=>{const option=document.createElement('option');option.value=provider.id;option.disabled=!provider.execution;option.textContent=provider.name+(provider.execution?'':' (unavailable)');return option}));
  const available=providers.find(provider=>provider.execution);$('provider').value='';
  $('request').hidden=!available;
  if(!available)$('output').textContent='No AI client is ready. Review Settings in WebDeviceCable.';
  commands = (await client.actions()).actions.filter(command => command.available && !command.sessions);
  $('command-request').hidden = !commands.length;
  $('command-select').replaceChildren(...commands.map(command => { const option = document.createElement('option'); option.value = command.id; option.textContent = command.title ?? command.id; return option; })); renderInputs();
  $('pair').hidden = true; $('manual').hidden = true; $('connect').hidden = true; $('connected').hidden = false; $('status').textContent = 'Connected to WebDeviceCable';
}
$('connect').onclick = async () => {
  $('error').textContent = ''; $('connect').disabled = true;
  try {
    if(!saved)throw new Error('Create an invitation in your isolated development panel.');
    await connected({connection:saved});
  }
  catch(error){$('error').textContent=error.message;}
  finally{$('connect').disabled=false;}
};

$('new-chat').onclick=async()=>{try{await chat?.close();chat=undefined;$('output').textContent='Ready.';}catch(error){errorMessage(error);}};
$('disconnect').onclick=async()=>{try{await client?.close();}catch(error){errorMessage(error);}finally{await store.clear().catch(errorMessage);saved=undefined;chat=undefined;client=undefined;$('connected').hidden=true;$('connect').hidden=true;$('manual').hidden=false;$('pair').hidden=false;$('connect').textContent='Connect to WebDeviceCable';$('status').textContent='Not connected';}};
