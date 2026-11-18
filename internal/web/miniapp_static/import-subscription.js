'use strict';

function readSubscriptionImport(fragment) {
  const payload = /^#(happ|incy)\/(.+)$/.exec(fragment);
  if (!payload) return null;
  try {
    const subscription = decodeURIComponent(payload[2]);
    if (subscription.length > 8192 || /[\u0000-\u0020\u007f]/.test(subscription)) return null;
    const url = new URL(subscription);
    if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) return null;
    const name = payload[1] === 'happ' ? 'Happ' : 'INCY';
    return {name, subscription, deeplink: payload[1] + '://add/' + subscription};
  } catch (_) {
    return null;
  }
}

const link = readSubscriptionImport(window.location.hash);
if (window.location.hash) history.replaceState(null, '', window.location.pathname);
if (link) {
  const en = (navigator.language || '').toLowerCase().startsWith('en');
  if (en) document.documentElement.lang = 'en';
  const label = (en ? 'Open ' : 'Открыть ') + link.name;
  const launch = document.getElementById('launch');
  const copy = document.getElementById('copy');
  const subscription = document.getElementById('subscription');
  const instructions = document.getElementById('instructions');
  launch.href = link.deeplink;
  launch.textContent = label;
  subscription.value = link.subscription;
  document.getElementById('title').textContent = en ? 'Add subscription' : 'Добавление подписки';
  instructions.textContent = en ? 'If the app does not open, copy the link and import it in the app.' : 'Если приложение не откроется, скопируйте ссылку и добавьте её в приложении.';
  copy.textContent = en ? 'Copy link' : 'Скопировать ссылку';
  copy.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(link.subscription);
      instructions.textContent = en ? 'Link copied.' : 'Ссылка скопирована.';
    } catch (_) {
      subscription.select();
      instructions.textContent = en ? 'Copy the selected link.' : 'Скопируйте выделенную ссылку.';
    }
  });
  document.getElementById('actions').hidden = false;
}
