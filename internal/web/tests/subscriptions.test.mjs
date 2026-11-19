import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('../miniapp_static/shared.js', import.meta.url), 'utf8');
function browser(response) {
  const context = {STATE:{lang:'ru'}, api:async()=>response,
    esc:value=>String(value).replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;')};
  vm.createContext(context);
  vm.runInContext(source, context);
  return context;
}

test('another tab cannot switch the current tab account', async()=>{
  const page=browser({enabled:true,selected_id:'b',items:[{id:'a',label:'A'},{id:'b',label:'B'}]});
  page.SUBSCRIPTIONS={enabled:true,selected_id:'a',items:[]};
  await page.loadSubscriptions();
  assert.equal(page.SUBSCRIPTIONS.selected_id,'a');
  assert.equal(page.subscriptionHeaders('/api/miniapp/checkout',{})['X-Subscription-ID'],'a');
  assert.equal(page.subscriptionHeaders('/api/miniapp/subscriptions',{})['X-Subscription-ID'],undefined);
});

test('a new login drops the previous customer account', async()=>{
  const page=browser({enabled:true,selected_id:'new',items:[{id:'new',label:'New customer'}]});
  page.SUBSCRIPTIONS={enabled:true,selected_id:'old',items:[]};
  await page.loadSubscriptions();
  assert.equal(page.SUBSCRIPTIONS.selected_id,'new');
  assert.equal(page.subscriptionHeaders('/api/miniapp/auth',{})['X-Subscription-ID'],undefined);
});

test('subscription labels are displayed as text',()=>{
  const page=browser({});
  page.SUBSCRIPTIONS={enabled:true,selected_id:'a',items:[{id:'a',label:'<img src=x onerror=alert(1)>'}]};
  const html=page.subscriptionPicker();
  assert.ok(!html.includes('<img'));
  assert.ok(html.includes('&lt;img'));
});

test('disabled mode keeps the existing interface',()=>{
  const page=browser({});
  assert.equal(page.subscriptionPicker(),'');
  assert.deepEqual(page.subscriptionHeaders('/api/miniapp/subscription',{}),{});
});
