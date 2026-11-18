// Общая часть двух дизайнов кабинета и мини-аппа (index.html и minimal.html).
// Сервер вставляет этот файл в страницу на место своей метки — отдельного
// запроса за ним нет. Здесь только функции, одинаковые в обоих дизайнах:
// правка делается один раз. Глобальные WEB, STATE, api и прочее объявляет
// основной скрипт страницы, функции обращаются к ним в момент вызова.

// Сторонний чат поддержки: только кабинет в браузере, скрипт ставится один раз.
var CHAT_ON=false;
function mountChat(src){
  if(CHAT_ON||!WEB||!src||!/^https:\/\//i.test(src))return;
  CHAT_ON=true;
  var s=document.createElement('script');s.src=src;s.async=true;document.body.appendChild(s);
}
function guestChat(){api('/api/cabinet/config').then(function(c){mountChat(c.chat_src);},function(){});}
// Параметры ссылки, по которой открыли кабинет: ?promo=, ?plan=, ?ref=, ?inv=
// или ?start=. Работают как /start в чате, их можно сочетать. Хранятся одной
// строкой через перевод строки.
function startParams(){
  var out=[];
  try{var q=new URLSearchParams(location.search);
    ['ref','inv','promo','plan'].forEach(function(k){var v=q.get(k);if(v)out.push(k+'_'+v);});
    var s=q.get('start');if(s)out.push(s);
  }catch(e){}
  // Разделитель — перевод строки: в коде промокода бывает что угодно, кроме
  // пробельных символов.
  return out.map(function(t){return t.replace(/\s/g,'').slice(0,64);}).filter(Boolean).join('\n');
}
// На сервер при входе уходит одно: приглашение в закрытый бот или
// пригласивший друг.
function serverStart(s){
  var t=String(s||'').split('\n');
  return t.filter(function(x){return x.indexOf('inv_')===0;})[0]||t.filter(function(x){return x.indexOf('ref_')===0;})[0]||'';
}
function setDesk(){DESK=!!(DESKMQ&&DESKMQ.matches);try{document.body.classList.toggle('desk',DESK);}catch(e){}}
function avaHTML(ini){return AVA&&AVA_ID&&AVA_ID===avaID()?'<img src="'+AVA+'" alt="">':esc(ini);}
function loadAva(){
  var id=avaID();
  if(!TOKEN||id<=0||AVA_ID===id)return;
  AVA_ID=id;if(AVA){try{URL.revokeObjectURL(AVA);}catch(e){}AVA='';}
  fetch('/api/miniapp/avatar',{headers:{'Authorization':'Bearer '+TOKEN}}).then(function(r){return r.ok?r.blob():null;}).then(function(b){
    if(!b||AVA_ID!==id||!window.URL)return;
    AVA=URL.createObjectURL(b);
    Array.prototype.forEach.call(document.querySelectorAll('[data-ava]'),function(e){e.innerHTML='<img src="'+AVA+'" alt="">';});
  }).catch(function(){});
}
function isTgLink(u){return /^(https:\/\/t\.me\/|tg:\/\/)/i.test(String(u||''));}
function planTitle(p){return (p.icon?p.icon+' ':'')+(p.name||p.code);}
function daysLeft(ts){var d=Math.ceil((ts*1000-Date.now())/86400000);return d>0?d:0;}
function payTabDrop(w){try{if(w&&!w.closed)w.close();}catch(e){}}
function legalDocs(){return (STATE.menu&&STATE.menu.legal)||[];}
// tgNonce — пара verifier/nonce: nonce уходит в Telegram и попадает в токен,
// verifier — только на наш сервер. Без verifier перехваченный токен не годится.
function tgNonce(){
  try{
    if(!(window.crypto&&crypto.subtle&&crypto.getRandomValues&&window.TextEncoder))return Promise.resolve({v:'',n:''});
    var b=new Uint8Array(32);crypto.getRandomValues(b);
    var v=btoa(String.fromCharCode.apply(null,b)).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
    return crypto.subtle.digest('SHA-256',new TextEncoder().encode(v)).then(function(h){
      return {v:v,n:Array.prototype.map.call(new Uint8Array(h),function(x){return ('0'+x.toString(16)).slice(-2);}).join('')};
    },function(){return {v:'',n:''};});
  }catch(e){return Promise.resolve({v:'',n:''});}
}

// Значение внутри onclick="…('значение')": экранируются и HTML, и строка JS,
// включая переводы строк — иначе обработчик кнопки ломается.
function jsq(s){return String(s==null?'':s).replace(/\\/g,'\\\\').replace(/'/g,"\\'").replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/\n/g,'\\n').replace(/\r/g,'\\r');}
// Копирование без Clipboard API (старые WebView).
function fallbackCopy(t){try{var ta=document.createElement('textarea');ta.value=t;ta.setAttribute('readonly','');ta.style.position='fixed';ta.style.left='-9999px';document.body.appendChild(ta);ta.select();ta.setSelectionRange(0,String(t).length);var ok=document.execCommand('copy');document.body.removeChild(ta);return ok;}catch(e){return false;}}

function subscriptionImportPage(deepLink, subscription){
  var match=/^(happ|incy):\/\/add\//i.exec(deepLink||'');
  if(!match||deepLink.slice(match[0].length)!==subscription||!/^https?:\/\//i.test(subscription))return '';
  return new URL('import-subscription.html',window.location.href).href+'#'+match[1].toLowerCase()+'/'+encodeURIComponent(subscription);
}
function connectImportActions(app, subscription, addLabel, browserLabel){
  if(!/^[a-z][a-z0-9+.-]*:/i.test(app.deeplink||'')||/^(javascript|vbscript|data|file):/i.test(app.deeplink))return '';
  var html='<a class="btn" href="'+esc(app.deeplink)+'" rel="noreferrer">'+IC.plus+esc(addLabel)+'</a>';
  var browser=tg&&tg.initData?subscriptionImportPage(app.deeplink,subscription):'';
  if(browser)html+='<button class="btn sec" onclick="openL(\''+jsq(browser)+'\')">'+esc(browserLabel)+'</button>';
  return html;
}
async function post(path,body){return api(path,{method:'POST',body:JSON.stringify(body||{})});}
var SUBSCRIPTIONS={enabled:false,items:[]};
async function loadSubscriptions(){
  var previous=SUBSCRIPTIONS.selected_id;
  try{SUBSCRIPTIONS=await api('/api/miniapp/subscriptions');}
  catch(e){if(e.status===404){SUBSCRIPTIONS={enabled:false,items:[]};return;}throw e;}
  if(SUBSCRIPTIONS.error)throw new Error(SUBSCRIPTIONS.error);
  if(previous&&(SUBSCRIPTIONS.items||[]).some(function(s){return s.id===previous;}))SUBSCRIPTIONS.selected_id=previous;
}
function subscriptionHeaders(path,h){
  if(path!=='/api/miniapp/subscriptions'&&path!=='/api/miniapp/auth'&&SUBSCRIPTIONS.enabled&&SUBSCRIPTIONS.selected_id)h['X-Subscription-ID']=SUBSCRIPTIONS.selected_id;
  return h;
}
function subscriptionPicker(){
  var data=SUBSCRIPTIONS||{},items=data.items||[];
  if(!data.enabled||!items.length)return '';
  var en=STATE.lang==='en',label=en?'Subscription':'Подписка';
  var h='<div class="card"><label><span>'+label+'</span><select aria-label="'+label+'" style="width:100%;margin-top:8px;padding:12px;border-radius:12px;font:inherit;color:inherit;background:transparent" onchange="pickSubscription(this.value,this)">';
  if(!data.selected_id)h+='<option value="">'+(en?'Select a subscription':'Выберите подписку')+'</option>';
  items.forEach(function(s){h+='<option value="'+esc(s.id)+'"'+(s.id===data.selected_id?' selected':'')+'>'+esc(s.label)+'</option>';});
  return h+'</select></label></div>';
}
async function pickSubscription(id,el){
  if(!id)return;
  el.disabled=true;
  try{var result=await post('/api/miniapp/subscriptions/select',{id:id});if(result.error)throw new Error(result.error);SUBSCRIPTIONS.selected_id=id;await boot();}
  catch(e){if(typeof toast==='function')toast(e.message,'err');else window.alert(e.message);}
  finally{el.disabled=false;}
}
function mkErr(st,m){var e=new Error(m||('HTTP '+st));e.status=st;return e;}
// true — ссылку открыл сам Telegram.
function openSmart(u){if(isTgLink(u)&&tg&&tg.openTelegramLink){try{tg.openTelegramLink(u);return true;}catch(e){}}openL(u);return false;}
