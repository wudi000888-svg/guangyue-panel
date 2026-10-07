// Run through the official Penpot MCP execute_code tool after loading assets.
// Creates native editable shapes, local library styles, tokens and prototypes.
storage.design = {
  canvas:'#F7F8F5',surface:'#FFFFFF',raised:'#EFF3ED',ink:'#203631',muted:'#66766D',border:'#E1E7DF',pine:'#173D36',accent:'#276A57',soft:'#E6F0E8',clay:'#B65D43',gold:'#EAE4C8',danger:'#A83F42',warning:'#946425',
};
storage.fonts={sans:penpot.fonts.findByName('Noto Sans SC'),serif:penpot.fonts.findByName('Noto Serif SC')};
storage.boards={};storage.links=[];storage.pages={};storage.components={};
const C=storage.design;
storage.shape=function(parent,name,x,y,w,h,fill=C.surface,r=12,border=C.border){
 const s=penpot.createRectangle();s.name=name;s.resize(w,h);s.fills=fill?[{fillColor:fill,fillOpacity:1}]:[];s.borderRadius=r;
 if(border)s.strokes=[{strokeColor:border,strokeWidth:1,strokeStyle:'solid',strokeAlignment:'inner'}];
 parent.appendChild(s);s.x=parent.x+x;s.y=parent.y+y;return s;
};
storage.text=function(parent,text,x,y,w=300,size=14,color=C.ink,weight='400',height=40,serif=false){
 const s=penpot.createText(text);s.name=text.split('\n')[0];
 const font=storage.fonts[serif?'serif':'sans'];if(font)font.applyToText(s,font.variants.find(v=>v.fontWeight===weight&&v.fontStyle==='normal'));
 s.fontSize=String(size);s.fontWeight=weight;s.lineHeight='1.5';s.fills=[{fillColor:color,fillOpacity:1}];s.resize(w,height);s.growType='fixed';parent.appendChild(s);s.x=parent.x+x;s.y=parent.y+y;return s;
};
storage.svg=function(parent,svg,name,x,y,w,h){
 const g=penpot.createShapeFromSvg(svg);g.name=name;g.resize(w,h);parent.appendChild(g);g.x=parent.x+x;g.y=parent.y+y;return g;
};
storage.icon=function(parent,name,x,y,size=20,color=C.muted){
 const svg=storage.assets.icons[name];if(!svg)return null;return storage.svg(parent,svg.replaceAll('currentColor',color),'Lucide / '+name,x,y,size,size);
};
storage.board=function(key,title,x,y,w,h){
 const b=penpot.createBoard();b.name=title;b.resize(w,h);b.x=x;b.y=y;b.fills=[{fillColor:C.canvas,fillOpacity:1}];b.clipContent=true;storage.boards[key]=b;return b;
};
storage.button=function(parent,label,x,y,w=144,primary=true,to=null){
 const b=penpot.createBoard();b.name='Button / '+label;b.resize(w,44);b.borderRadius=8;b.fills=[{fillColor:primary?C.accent:C.surface,fillOpacity:1}];
 if(!primary)b.strokes=[{strokeColor:C.border,strokeWidth:1,strokeStyle:'solid'}];parent.appendChild(b);b.x=parent.x+x;b.y=parent.y+y;
 const tx=storage.text(b,label,8,12,w-16,14,primary?'#FFFFFF':C.ink,'500',24);tx.align='center';
 if(to)storage.links.push({shape:b,to});return b;
};
storage.badge=function(parent,label,x,y,tone='success',w=86){
 const fill=tone==='danger'?'#F8E9E8':tone==='warning'?'#FBF1DF':tone==='neutral'?C.raised:C.soft;
 const color=tone==='danger'?C.danger:tone==='warning'?C.warning:tone==='neutral'?C.muted:C.accent;
 storage.shape(parent,'Status / '+label,x,y,w,28,fill,6,null);return storage.text(parent,label,x+10,y+5,w-20,12,color,'500',20);
};
storage.input=function(parent,label,value,x,y,w=344){
 storage.text(parent,label,x,y,w,13,C.ink,'500',24);storage.shape(parent,'Input / '+label,x,y+31,w,46,C.surface,8);storage.text(parent,value,x+13,y+43,w-26,14,C.muted,'400',24);
};
storage.header=function(b,title,desc,member=false){
 const mobile=b.width<600;
 if(mobile){
  storage.shape(b,'Mobile top bar',0,0,b.width,66,C.surface,0,null);storage.svg(b,storage.assets.seal,'Brand / Moon seal',18,17,32,32);storage.text(b,'广月',60,18,120,19,C.pine,'600',30);storage.icon(b,'menu',338,22,23,C.pine);
  storage.text(b,title,20,94,350,26,C.ink,'600',40);storage.text(b,desc,20,139,350,13,C.muted,'400',48);
  if(member){storage.shape(b,'Mobile bottom navigation',0,b.height-68,b.width,68,C.surface,0,C.border);['house','shopping-bag','wallet','receipt','circle-help'].forEach((ic,i)=>{storage.icon(b,ic,27+i*77,b.height-56,21,i===0?C.accent:C.muted);storage.text(b,['服务','套餐','钱包','订单','帮助'][i],15+i*77,b.height-30,54,12,i===0?C.accent:C.muted,'500',22).align='center';});}
  return {x:20,y:204,w:350};
 }
 storage.shape(b,'Sidebar',0,0,224,b.height,C.pine,0,null);storage.svg(b,storage.assets.seal,'Brand / Moon seal',24,28,40,40);storage.text(b,'广月面板',76,30,134,20,'#F3F0E1','600',32,true);storage.text(b,'GUANGYUE',77,62,130,12,'#B9CBBE','500',24);
 const nav=member?['我的服务','选购套餐','我的订单','我的钱包','客户端','工单帮助']:['工作台','用户管理','套餐与销售','订单管理','本地节点','子站节点','IP 与出口','节点组','监控与任务','工单与消息','系统设置'];
 const icons=member?['house','shopping-bag','receipt','wallet','download','circle-help']:['house','users','package','receipt','server','network','globe','layers','activity','messages-square','settings'];
 nav.forEach((name,i)=>{const yy=121+i*52;if(i===0)storage.shape(b,'Active navigation',12,yy-6,200,43,'#2B5145',8,null);storage.icon(b,icons[i],25,yy+5,18,'#BDCEC1');storage.text(b,name,58,yy+2,152,13,i===0?'#FFFFFF':'#BDCEC1',i===0?'500':'400',30);});
 storage.text(b,'月庭 · 清晰而从容',24,b.height-59,180,12,'#B9CBBE','400',24);
 storage.shape(b,'Top bar',224,0,b.width-224,74,C.surface,0,C.border);storage.text(b,member?'个人中心':'广州主站 / 管理工作区',256,25,360,13,C.muted,'400',30);storage.shape(b,'Search',b.width-448,17,232,39,C.canvas,7,C.border);storage.icon(b,'search',b.width-436,27,17);storage.text(b,'搜索功能  ⌘K',b.width-409,25,178,12,C.muted,'400',25);storage.icon(b,'bell',b.width-163,28,20);storage.icon(b,'sun',b.width-115,28,20);storage.shape(b,'Account avatar',b.width-65,20,34,34,C.soft,17,null);storage.text(b,'月',b.width-56,25,23,14,C.accent,'500',24);
 storage.text(b,title,260,108,720,28,C.ink,'600',44);storage.text(b,desc,260,159,b.width-304,14,C.muted,'400',43);return {x:260,y:224,w:b.width-296};
};
storage.card=function(b,title,desc,x,y,w,h,action=null){
 storage.shape(b,title,x,y,w,h);storage.text(b,title,x+22,y+20,w-44,18,C.ink,'600',33);if(desc)storage.text(b,desc,x+22,y+62,w-44,13,C.muted,'400',58);if(action)storage.button(b,action.label,x+22,y+h-66,Math.min(w-44,178),action.primary!==false,action.to);return {x:x+22,y:y+126,w:w-44};
};
storage.page=function(key,name){const p=penpot.createPage();p.name=name;storage.pages[key]=p;return penpot.openPage(p);};

penpot.currentPage.name='01 · 基础体系与组件';storage.pages.foundations=penpot.currentPage;
const foundation=storage.board('foundations','月庭 / Foundations',0,0,1440,1080);
storage.text(foundation,'月庭 · 广月面板',48,42,980,36,C.pine,'500',62,true);
storage.text(foundation,'让复杂的运营清晰，让每一次连接从容。',48,112,1040,18,C.muted,'400',36);
const catalog=penpot.library.local.tokens,tokenSet=catalog.addSet({name:'moon-courtyard/light'});if(!tokenSet.active)tokenSet.toggleActive();
Object.entries(C).forEach(([key,value],i)=>{
 const color=penpot.library.local.createColor();color.name='Moon Courtyard / '+key;color.color=value;
 const token=tokenSet.addToken({type:'color',name:'color.'+key,value});
 const swatch=storage.shape(foundation,'Palette / '+key,48+(i%7)*194,190+Math.floor(i/7)*130,170,72,value,12,null);swatch.applyToken(token,['fill']);
 storage.text(foundation,key,48+(i%7)*194,269+Math.floor(i/7)*130,170,13,C.ink,'500',25);storage.text(foundation,value,48+(i%7)*194,294+Math.floor(i/7)*130,170,12,C.muted,'400',22);
});
[['spacing.1','4'],['spacing.2','8'],['spacing.3','12'],['spacing.4','16'],['spacing.6','24'],['spacing.8','32']].forEach(([name,value])=>tokenSet.addToken({type:'spacing',name,value}));
[['radius.control','8'],['radius.card','12'],['radius.dialog','16']].forEach(([name,value])=>tokenSet.addToken({type:'borderRadius',name,value}));
[['type.caption','12'],['type.body','14'],['type.section','18'],['type.page','28']].forEach(([name,value])=>tokenSet.addToken({type:'fontSizes',name,value}));
storage.text(foundation,'文字与层级',48,467,350,24,C.ink,'600',40);storage.text(foundation,'页面标题 / 28',48,530,430,28,C.ink,'600',45);storage.text(foundation,'模块标题 / 18',48,589,430,18,C.ink,'600',35);storage.text(foundation,'正文 / 14 · 先说结果，再给下一步',48,639,500,14,C.ink,'400',30);storage.text(foundation,'说明 / 13 · 关键状态不只依赖颜色',48,682,500,13,C.muted,'400',30);
const primary=storage.button(foundation,'保存并应用',674,523,184,true);const secondary=storage.button(foundation,'取消',883,523,132,false);storage.button(foundation,'保存中…',1043,523,172,true);
[primary,secondary].forEach((b,i)=>{const component=penpot.library.local.createComponent([b]);component.name=i===0?'Button / Primary':'Button / Secondary';storage.components[i===0?'primary':'secondary']=component;});
storage.badge(foundation,'已生效',674,595,'success');storage.badge(foundation,'等待同步',781,595,'warning',102);storage.badge(foundation,'需要处理',903,595,'danger',102);storage.badge(foundation,'未连接',1025,595,'neutral');
storage.input(foundation,'名称','香港 · 海棠庭',674,651,500);storage.text(foundation,'表单目标 44px · 页面间距 24px · 所有资源本地加载',674,759,650,13,C.muted,'400',38);
storage.shape(foundation,'Notice',48,818,1344,148,C.soft,12,null);storage.icon(foundation,'shield-check',70,844,24,C.accent);storage.text(foundation,'保留全部能力，按任务组织复杂度',111,840,1230,18,C.accent,'600',36);storage.text(foundation,'管理员：资源 → 套餐 → 销售 → 成员生效\n成员：选择套餐 → 确认订单 → 开通 → 按设备连接 → 查看用量与售后',111,885,1180,14,C.ink,'400',63);
penpot.viewport.zoomIntoView([foundation]);
return {page:penpot.currentPage.id,board:foundation.id,tokens:tokenSet.tokens.length,components:Object.keys(storage.components)};
