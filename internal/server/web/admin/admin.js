// admin.js — the inventory, and the two things only this port can do.
//
// It talks to its OWN origin and nothing else. The admin port and the public
// port are two services that happen to share a data directory, and a page here
// reaching into the public one would be the first step towards the admin
// interface needing to be reachable from outside, which is exactly what its
// whole design avoids.

const view = {
  list: document.getElementById('list'),
  trash: document.getElementById('trash'),
  search: document.getElementById('q'),
  count: document.getElementById('count'),
  policy: document.getElementById('policy'),
  prev: document.getElementById('prev'),
  next: document.getElementById('next'),
};

let page = 1;

function el(tag, attrs, children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'class') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? '' : value);
  }
  for (const child of children || []) if (child) node.appendChild(child);
  return node;
}

async function call(method, path) {
  const response = await fetch(path, { method });
  const parsed = await response.json().catch(() => null);
  if (!response.ok || (parsed && parsed.ok === false)) {
    throw new Error((parsed && parsed.error) || response.statusText);
  }
  return parsed;
}

function human(bytes) {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return Math.round(bytes / 1024) + ' kB';
  return (bytes / 1024 / 1024).toFixed(1) + ' MB';
}

function when(stamp) {
  if (!stamp) return '';
  const date = new Date(stamp);
  return isNaN(date) ? stamp : date.toLocaleString();
}

async function refresh() {
  const params = new URLSearchParams({ page: String(page) });
  if (view.search.value.trim()) params.set('q', view.search.value.trim());
  let answer;
  try {
    answer = await call('GET', '/api/profiles?' + params);
  } catch (error) {
    view.list.textContent = error.message;
    return;
  }
  page = answer.page;
  view.policy.textContent = 'published: ' + answer.policy;
  view.count.textContent = `${answer.total} CV(s) — page ${answer.page}/${answer.pages}`;

  if (!answer.profiles.length) {
    view.list.textContent = '';
    view.list.appendChild(el('div', { class: 'empty', text: 'Nothing here.' }));
    return;
  }

  const rows = answer.profiles.map((p) => el('tr', {}, [
    el('td', {}, [
      el('div', { text: p.name }),
      el('code', { class: 'policy', text: p.slug }),
    ]),
    el('td', {}, [
      el('span', { class: 'tag' + (p.public ? ' public' : ''), text: p.public ? 'published' : 'by link only' }),
      document.createTextNode(' '),
      el('span', { class: 'tag', text: (p.languages || []).map((l) => l.lang).join(', ') }),
    ]),
    el('td', { class: 'links' }, [
      el('code', { text: p.links.edit }),
      el('code', { text: p.links.read }),
    ]),
    el('td', { text: `${human(p.bytes)} · ${p.history} entries` }),
    el('td', { text: when(p.updatedAt) }),
    el('td', {}, [
      el('a', { class: 'tag', href: `/view/${p.slug}/cv.html`, target: '_blank', text: 'page' }),
      document.createTextNode(' '),
      el('a', { class: 'tag', href: `/view/${p.slug}/cv.pdf`, target: '_blank', text: 'pdf' }),
      document.createTextNode(' '),
      el('button', {
        text: 'new edit link',
        onclick: () => act(`/api/p/${p.slug}/links/rotate?mode=edit`, 'POST',
          `Renew the edit link of “${p.name}”? Every copy already handed out stops working.`),
      }),
      el('button', {
        class: 'danger', text: 'delete',
        onclick: () => act(`/api/p/${p.slug}`, 'DELETE',
          `Delete “${p.name}”? It is set aside first, and can be restored until it expires.`),
      }),
    ]),
  ]));

  view.list.textContent = '';
  view.list.appendChild(el('table', {}, [
    el('thead', {}, [el('tr', {}, ['CV', 'access', 'links', 'size', 'last change', '']
      .map((label) => el('th', { text: label })))]),
    el('tbody', {}, rows),
  ]));
}

async function act(path, method, question) {
  if (question && !confirm(question)) return;
  try {
    await call(method, path);
    await refresh();
    await refreshTrash();
  } catch (error) {
    alert(error.message);
  }
}

async function refreshTrash() {
  let answer;
  try {
    answer = await call('GET', '/api/trash');
  } catch (error) {
    view.trash.textContent = error.message;
    return;
  }
  view.trash.textContent = '';
  if (!answer.entries || !answer.entries.length) {
    view.trash.appendChild(el('div', { class: 'empty', text: 'Nothing set aside.' }));
    return;
  }
  const rows = answer.entries.map((entry) => el('tr', {}, [
    el('td', {}, [el('div', { text: entry.name }), el('code', { class: 'policy', text: entry.slug })]),
    el('td', { text: `deleted ${when(entry.deletedAt)}` }),
    el('td', { text: `erased ${when(entry.expiresAt)}` }),
    el('td', { text: human(entry.bytes) }),
    el('td', {}, [
      el('button', { text: 'restore', onclick: () => act(`/api/trash/${entry.slug}/restore`, 'POST') }),
      el('button', {
        class: 'danger', text: 'erase now',
        onclick: () => act(`/api/trash/${entry.slug}`, 'DELETE',
          `Erase “${entry.name}” for good? This cannot be undone.`),
      }),
    ]),
  ]));
  view.trash.appendChild(el('table', {}, [
    el('thead', {}, [el('tr', {}, ['CV', 'when', 'until', 'size', '']
      .map((label) => el('th', { text: label })))]),
    el('tbody', {}, rows),
  ]));
}

let typing = 0;
view.search.addEventListener('input', () => {
  clearTimeout(typing);
  typing = setTimeout(() => { page = 1; refresh(); }, 200);
});
view.prev.addEventListener('click', () => { if (page > 1) { page -= 1; refresh(); } });
view.next.addEventListener('click', () => { page += 1; refresh(); });

refresh();
refreshTrash();
