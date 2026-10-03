(() => {
  const target = __TARGET_JSON__;
  const name = el => (el.getAttribute('aria-label') ||
    (el.getAttribute('aria-labelledby') || '').split(' ').map(id => document.getElementById(id)?.textContent || '').join(' ').trim() ||
    Array.from(el.labels || []).map(label => label.textContent).join(' ').trim() ||
    el.placeholder || (['button', 'submit'].includes(el.type) ? el.value : '') ||
    (['BUTTON', 'A'].includes(el.tagName) ? el.textContent : '') || el.title || '').trim().slice(0, 400);
  const roles = {
    button: 'button,input[type=submit],input[type=button],[role=button]',
    link: 'a,[role=link]',
    textbox: 'input,textarea,[role=textbox]',
    combobox: 'select,[role=combobox]',
    checkbox: 'input[type=checkbox],[role=checkbox]',
    radio: 'input[type=radio],[role=radio]'
  };
  let matches = [];
  if (target.selector) {
    try { matches = Array.from(document.querySelectorAll(target.selector)); } catch {}
  } else if (target.role && target.name) {
    matches = Array.from(document.querySelectorAll(roles[target.role] || '[role]'));
  }
  matches = matches.filter(el => el.getClientRects().length && !el.disabled && (!target.name || name(el) === target.name.trim()));
  if (matches.length > 1 && target.context) matches = matches.filter(el => (el.closest('tr,[role=row],article')?.textContent || '').includes(target.context));
  if (matches.length !== 1) return { count: matches.length };
  const el = matches[0];
  if (el.type === 'password' || /password|passwd|otp|one.?time|verification.?code|credit.?card|card.?number|token|secret/i.test([el.name, el.id, el.autocomplete].join(' '))) return { count: 0 };
  // This path is created from a fresh unique semantic match, never persisted.
  const parts = [];
  for (let node = el; node && node.nodeType === 1; node = node.parentElement) {
    if (node === document.documentElement) { parts.unshift('html'); break; }
    const siblings = Array.from(node.parentElement?.children || []).filter(sibling => sibling.tagName === node.tagName);
    parts.unshift(node.tagName.toLowerCase() + ':nth-of-type(' + (siblings.indexOf(node) + 1) + ')');
  }
  return { count: 1, selector: parts.join(' > ') };
})()
