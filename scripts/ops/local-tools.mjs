import {spawnSync} from 'node:child_process';
const quote=value=>"'"+String(value).replaceAll("'","'\\''")+"'";
// Passing a shell program over stdin avoids Windows/WSL double interpretation
// of quotes. Payloads travel through stdin and never become command arguments.
export function runWSL(args,input){
 const command=args.map(quote).join(' '),script='set -euo pipefail\n'+(input===undefined?command:`printf '%s' ${quote(Buffer.from(input).toString('base64'))} | base64 --decode | ${command}`)+'\n';
 return spawnSync('wsl.exe',['-d','Ubuntu-24.04','--','bash','-s'],{input:script,encoding:null,maxBuffer:64*1024*1024,stdio:['pipe','pipe','pipe']});
}
