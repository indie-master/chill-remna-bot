import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

const asset = name => readFileSync(new URL('../miniapp_static/' + name, import.meta.url), 'utf8');
const subscription = 'https://subscriptions.example.invalid/selected?mode=full&client=test#name';
const esc = value => String(value).replaceAll('&','&amp;').replaceAll('"','&quot;').replaceAll('<','&lt;').replaceAll('>','&gt;');

function connectPage(design, api) {
  const views = [];
  const arg = {};
  const page = vm.createContext({
    AbortController, setTimeout, clearTimeout, api, esc, IC:{plus:'+',copy:'copy',download:'download',globe:'OS'},
    view:html=>views.push(html), haptic(){}, boot(){}, WEB:false, DESK:false, tg:{platform:'ios'},
    STATE:{lang:'ru'}, PAGE:'home', CUR:{name:'connect',arg}, SCREENS:{},
    document:{getElementById(){return null;}}, deskSplit:(first,second)=>first+second,
    T:key=>key, backBtn:()=>'<button>Назад</button>', detectOS:()=>'ios', rerender(){}, drawQR(){},
  });
  vm.runInContext(asset('shared.js'),page);
  const html = asset(design==='classic'?'index.html':'minimal.html');
  const start = design==='classic'?'var CONNECT=null':'function cnPlatforms(c)';
  const end = design==='classic'?'/* ---- account:':'function cnPlat(k)';
  assert.ok(html.indexOf(start)>=0 && html.indexOf(end)>html.indexOf(start));
  vm.runInContext(html.slice(html.indexOf(start),html.indexOf(end)),page);
  return {page,views,open:()=>design==='classic'?page.openConnect():page.SCREENS.connect(arg)};
}

test('Happ and INCY use native links and reveal a manual copy option without opening a browser', () => {
  const {page} = connectPage('classic');
  for(const scheme of ['happ','incy']) {
    const link = scheme+'://add/'+subscription;
    const actions = page.connectImportActions({deeplink:link},subscription,'Add','Browser');
    assert.ok(actions.includes('href="'+esc(link)+'"'));
    assert.match(actions, /rel="noreferrer"[^>]+showImportHelp\(this\)/);
    assert.match(actions, /role="status" hidden>/);
    assert.match(actions, /onclick="copy\(/);
    assert.doesNotMatch(actions, /import-subscription|window\.open|openLink|Browser/);
  }
  for(const invalid of ['https://example.invalid', 'javascript://alert(1)', 'vbscript://test', 'file:///data']) {
    assert.equal(page.connectImportActions({deeplink:invalid},subscription,'Add'),'');
  }
  assert.equal(page.connectImportActions({deeplink:'happ://add/'+subscription},'','Add'),'');
  const anchor = {nextElementSibling:{hidden:true}};
  page.showImportHelp(anchor);
  assert.equal(anchor.nextElementSibling.hidden,false);
});

for(const design of ['classic','minimal']) {
  test(design+' stays on connect after failure and retries with the selected subscription',async()=>{
    let attempts=0;
    const {open,views} = connectPage(design,async(path,{signal})=>{
      assert.equal(path,'/api/miniapp/connect');
      assert.ok(signal instanceof AbortSignal);
      attempts++;
      if(attempts===1)throw Object.assign(new Error(subscription),{status:502});
      if(attempts===2)return {unavailable:true};
      return {sub_url:subscription,ios:[{name:'Happ',deeplink:'happ://add/'+subscription}]};
    });
    const pending = open();
    assert.match(views.at(-1),/Загружаем/);
    await pending;
    assert.match(views.at(-1),/HTTP 502/);
    assert.match(views.at(-1),/Повторить/);
    assert.doesNotMatch(views.at(-1),/subscriptions\.example\.invalid/);
    await open();
    assert.match(views.at(-1),/Сервис подписок временно недоступен/);
    await open();
    assert.match(views.at(-1),/happ:\/\/add\/https:/);
    assert.doesNotMatch(views.at(-1),/import-subscription|Импорт через браузер/);
    assert.equal(attempts,3);
  });

  test(design+' ignores a response after the customer goes back',async()=>{
    let complete;
    const {page,open,views} = connectPage(design,()=>new Promise(resolve=>{complete=resolve;}));
    const pending = open();
    page.PAGE='home';
    page.CUR={name:'home',arg:{}};
    complete({unavailable:true});
    await pending;
    assert.equal(views.length,1);
  });
}
