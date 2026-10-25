// Development-only JSONL example. WDC itself does not need Node.js.
import { createInterface } from 'node:readline';
const history = [];
const emit = frame => process.stdout.write(JSON.stringify(frame)+'\n');
for await (const line of createInterface({input:process.stdin})) {
  const turn = JSON.parse(line);
  if(turn.v !== 1 || turn.type !== 'turn') process.exit(2);
  const base={v:1,id:turn.id};
  if(turn.prompt === '/fail') {
    emit({...base,type:'error',error:{code:'rate_limited',message:'Example service limit',raw:'Example diagnostic: Bearer sk-example-secret-12345'}});
    break;
  }
  history.push(turn.prompt ?? '');
  emit({...base,type:'output',delta:'Working…\n'});
  await new Promise(resolve=>setTimeout(resolve,400));
  emit({...base,type:'output',delta:JSON.stringify({messages:history,pid:process.pid,arguments:turn.arguments ?? {}})});
  emit({...base,type:'done'});
}
