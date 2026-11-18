import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

const asset = name => readFileSync(new URL('../miniapp_static/' + name, import.meta.url), 'utf8');
const subscription = 'https://subscriptions.example.invalid/selected?mode=full&client=test#name';

test('Telegram import uses a same-origin browser handoff with a fragment', () => {
  const page = {URL, window:{location:{href:'https://shop.example.invalid/miniapp/'}}, tg:{initData:'synthetic'},
    IC:{plus:'+'}, esc:s=>String(s).replaceAll('&','&amp;').replaceAll('"','&quot;')};
  vm.createContext(page);
  vm.runInContext(asset('shared.js'),page);
  const link = 'happ://add/' + subscription;
  const target = page.subscriptionImportPage(link, subscription);
  assert.equal(target, 'https://shop.example.invalid/miniapp/import-subscription.html#happ/' + encodeURIComponent(subscription));
  assert.equal(page.subscriptionImportPage(link,'https://subscriptions.example.invalid/wrong'),'');
  assert.equal(page.connectImportActions({deeplink:'javascript:alert(1)'},subscription,'Add','Browser'),'');
  const actions = page.connectImportActions({deeplink:link},subscription,'Add','Browser');
  assert.match(actions, /<a[^>]+href="happ:\/\/add\/https:\/\//);
  assert.match(actions, /import-subscription\.html#happ\//);
  page.tg = null;
  assert.doesNotMatch(page.connectImportActions({deeplink:link},subscription,'Add','Browser'),/import-subscription/);
});

test('import page validates schemes, then opens precisely the selected subscription', () => {
  const elements = Object.fromEntries(['actions','launch','copy','subscription','title','instructions'].map(id=>[id,{addEventListener(){}}]));
  let historyPath;
  const page = {URL, window:{location:{hash:'#incy/'+encodeURIComponent(subscription),pathname:'/cabinet/import-subscription.html'}},
    history:{replaceState(_state,_title,path){historyPath = path;}}, navigator:{language:'ru'},
    document:{documentElement:{},getElementById:id=>elements[id]}};
  vm.createContext(page);
  vm.runInContext(asset('import-subscription.js'),page);
  assert.equal(historyPath,'/cabinet/import-subscription.html');
  assert.equal(elements.launch.href,'incy://add/'+subscription);
  assert.equal(elements.subscription.value,subscription);
  assert.equal(elements.actions.hidden,false);
  for(const value of ['#bad/https%3A%2F%2Fexample.invalid','#happ/javascript%3Aalert(1)','#incy/http%3A%2F%2Fname%3Apassword%40example.invalid','#incy/%ZZ']) {
    assert.equal(page.readSubscriptionImport(value),null);
  }
});
