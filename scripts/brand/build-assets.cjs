// 用法：node scripts/brand/build-assets.cjs <sharp模块路径>；不需要联网。
const fs = require('node:fs');
const path = require('node:path');
const sharp = require(process.argv[2] || 'sharp');
const root = path.resolve(__dirname, '../..');
const out = path.join(root, 'frontend/public/brand');
const source = JSON.parse(fs.readFileSync(path.join(__dirname, 'geometry.json'), 'utf8'));
const {mark, wordmark} = source;
const colors = {primary:['#AA3828','#232422'],dark:['#F0AD9B','#F0EEE6'],black:['#000000','#000000'],white:['#FFFFFF','#FFFFFF'],vermilion:['#AA3828','#AA3828']};
const svg = (w,h,body) => `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" role="img" aria-label="知数"><title>知数</title>${body}</svg>\n`;
const shape = (d,fill,transform='') => `<path fill="${fill}" d="${d}"${transform?` transform="${transform}"`:''}/>`;
const layouts = {
  mark:(m,t)=>[100,100,shape(mark,m)],
  horizontal:(m,t)=>[290,100,shape(mark,m)+shape(wordmark,t,'translate(128 11) scale(.78)')],
  stacked:(m,t)=>[240,260,shape(mark,m,'translate(70 16)')+shape(wordmark,t,'translate(20 145)')],
  signature:(m,t)=>[340,120,shape(mark,m,'translate(0 10)')+shape(wordmark,t,'translate(120 0)')+shape(source.pinyin,t,'translate(124 101)')],
};
(async()=>{
fs.mkdirSync(out,{recursive:true});
fs.writeFileSync(path.join(out,'zhishu-symbols.svg'),`<svg xmlns="http://www.w3.org/2000/svg"><symbol id="mark" viewBox="0 0 100 100"><path d="${mark}"/></symbol><symbol id="wordmark" viewBox="0 0 204 100"><path d="${wordmark}"/></symbol></svg>\n`);
for(const [variant,[m,t]] of Object.entries(colors)) for(const [layout,draw] of Object.entries(layouts)) {
  const [w,h,body]=draw(m,t), text=svg(w,h,body), name=`zhishu-${layout}-${variant}`;
  fs.writeFileSync(path.join(out,name+'.svg'),text);
  const width=layout==='mark'?1024:2400;
  await sharp(Buffer.from(text)).resize({width}).png().toFile(path.join(out,name+'.png'));
}
const icon=svg(100,100,shape(mark,'#AA3828'));
fs.writeFileSync(path.join(out,'favicon.svg'),icon);
for(const size of [16,32,48,64,128,256]) await sharp(Buffer.from(icon)).resize(size,size).png().toFile(path.join(out,`favicon-${size}.png`));
const sizes=[16,32,48,256], chunks=sizes.map(size=>fs.readFileSync(path.join(out,`favicon-${size}.png`)));
const header=Buffer.alloc(6+16*sizes.length);header.writeUInt16LE(1,2);header.writeUInt16LE(sizes.length,4);
let offset=header.length;
chunks.forEach((chunk,i)=>{const p=6+i*16;header[p]=sizes[i]%256;header[p+1]=sizes[i]%256;header.writeUInt16LE(1,p+4);header.writeUInt16LE(32,p+6);header.writeUInt32LE(chunk.length,p+8);header.writeUInt32LE(offset,p+12);offset+=chunk.length});
const ico=Buffer.concat([header,...chunks]);fs.writeFileSync(path.join(out,'favicon.ico'),ico);fs.writeFileSync(path.join(out,'../favicon.ico'),ico);
for(const [name,size] of [['apple-touch-icon',180],['icon-192',192],['icon-512',512]]) {
  // 触屏与安装图标留足安全区，背景不透明。
  await sharp(Buffer.from(svg(100,100,'<path fill="#F8F7F2" d="M0 0h100v100H0z"/>'+shape(mark,'#AA3828','translate(16 16) scale(.68)')))).resize(size,size).png().toFile(path.join(out,name+'.png'));
}
fs.writeFileSync(path.join(out,'site.webmanifest'),JSON.stringify({name:'知数 · 专业数据管理平台',short_name:'知数',lang:'zh-CN',start_url:'/',display:'browser',background_color:'#EEECE5',theme_color:'#EEECE5',icons:[{src:'/brand/icon-192.png',sizes:'192x192',type:'image/png',purpose:'any'},{src:'/brand/icon-512.png',sizes:'512x512',type:'image/png',purpose:'any'}]},null,2)+'\n');
console.log('已生成20组SVG/透明PNG，以及浏览器和触屏图标。');
})().catch(e=>{console.error(e);process.exitCode=1});
