// Load once, then open and finish one page per execute_code call.
// Separate page changes from mutations so Penpot can settle its layer tree.
storage.finishPage=function(pageKey){
 const page=storage.pages[pageKey];if(penpot.currentPage.id!==page.id)throw new Error('Open the page before finishing it');
 const children=s=>['board','group','boolean'].includes(s.type)?s.children:[];
 const visit=(s,fn)=>{fn(s);children(s).forEach(c=>visit(c,fn));};
 const core=['login','register','overview','sites','plans','nodes','settings','member','connect','shop','order','wallet','tickets','monitor'];
 const nav={'工作台':'overview','用户管理':'users','套餐与销售':'plans','订单管理':'order','本地节点':'nodes','子站节点':'sites','IP 与出口':'ip','节点组':'groups','监控与任务':'monitor','工单与消息':'tickets','系统设置':'settings','我的服务':'member','选购套餐':'shop','我的订单':'order','我的钱包':'wallet','客户端':'connect','工单帮助':'tickets'};
 const targets={'新用户？创建账号':'register','已有账号？返回登录':'login','登录':'overview','创建账号':'member','开始连接':'connect','选择设备与客户端':'connect','查看套餐与订单':'order','选择套餐':'order','复制订阅并导入':'member','确认支付 ¥29.00':'member','查看我的服务':'member','查看待办':'tickets','连接一个子站':'sites','查看共享节点':'shared','保存挂载':'sites','前往套餐选择节点':'plans','新增线路':'editor','保存套餐内容':'sales','前往套餐':'plans'};
 const localBoards=Object.entries(storage.boards).filter(([key])=>{
  if(key==='foundations')return pageKey==='foundations';
  const mobile=key.startsWith('mobile-'),bare=key.slice(mobile?7:8);
  return pageKey===(core.includes(bare)?mobile?'mobile':'desktop':mobile?'mobile-extended':'desktop-extended');
 });
 const localBoardIDs=new Set(localBoards.map(([,board])=>board.id));
 localBoards.forEach(([key,b])=>{
  const mode=key.startsWith('mobile-')?'mobile':'desktop',bare=key.slice(mode==='mobile'?7:8);
  if(mode==='mobile'&&core.includes(bare))b.y=Math.floor(core.indexOf(bare)/3)*1240;
  visit(b,s=>{if(s.type==='text'&&s.fontSize<12)s.fontSize='12';});
  if(bare==='plans'){
   const card=children(b).find(s=>s.name==='Plan editor');if(card&&card.height<642)card.resize(card.width,642);
   const save=children(b).find(s=>s.name==='Button / 保存套餐内容');if(save&&card)save.y=card.y+574;
  }
  if(key==='desktop-member'){const card=children(b).find(s=>s.name==='Current plan');if(card)card.resize(card.width,304);}
  if(key==='desktop-connect'){
   const qr=children(b).find(s=>s.name==='QR placeholder');if(qr){const x=qr.x-b.x,y=qr.y-b.y;qr.remove();storage.svg(b,storage.assets.qr,'QR / Sample subscription',x,y,162,162);}
  }
  visit(b,s=>{
   const label=s.name.replace('Button / ',''),target=s.type==='text'&&nav[s.name]||targets[label];
   s.interactions.forEach(interaction=>s.removeInteraction(interaction));
   const destination=target&&storage.boards[mode+'-'+target];
   // Penpot's prototype navigation resolves boards inside the current page.
   if(destination&&localBoardIDs.has(destination.id)){
    s.addInteraction('click',{type:'navigate-to',destination});
   }
  });
  if(mode==='desktop'){
   const selected={sales:'plans',grants:'plans',editor:'nodes',shared:'sites',budget:'sites',imports:'ip',public:'ip',report:'monitor',tokens:'sites',tasks:'monitor',system:'settings',redeem:'plans',subscriptions:'member',inbox:'tickets'}[bare]||bare;
   const entries=children(b).filter(s=>s.type==='text'&&nav[s.name]&&s.x<b.x+224);
   const active=children(b).find(s=>s.name==='Active navigation'),text=entries.find(s=>nav[s.name]===selected);
   if(active&&text)active.y=text.y-8;
   entries.forEach(s=>{s.fills=[{fillColor:s===text?'#FFFFFF':'#BDCEC1',fillOpacity:1}];});
  }
 });
 page.flows.forEach(flow=>flow.remove());
 const flow=(label,key)=>{const value=page.createFlow(label,storage.boards[key]);value.name=label;};
 if(pageKey==='desktop'){flow('管理员 · 资源到套餐','desktop-overview');flow('成员 · 购买到连接','desktop-member');}
 if(pageKey==='mobile')flow('手机 · 套餐到连接','mobile-member');
 if(pageKey==='desktop-extended')flow('桌面 · 管理工作区','desktop-shared');
 if(pageKey==='mobile-extended')flow('手机 · 管理工作区','mobile-shared');
 return {page:page.name,boards:localBoards.length};
};
return {finisherReady:true};
