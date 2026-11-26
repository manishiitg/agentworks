'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { notifyRoute, notificationWindowActive } = require('./notify');

// PLAT-746: a company-server notification must not discard its agent target.
test('notification click preserves exact Main and side chat without arbitrary routes', () => {
  for (const agent of ['main', 'fork-example', 'agent-123']) {
    const route = '/projects/example?agent=' + agent;
    assert.equal(notifyRoute(route, true), route);
    assert.equal(notifyRoute(route, false), route);
  }
  assert.equal(notifyRoute('/projects/example?agent=fork-example&view=terminal', true), '/projects/example?agent=fork-example');
  assert.equal(notifyRoute('/projects/example?agent=..%2Fsecret', true), '/projects/example');
  assert.equal(notifyRoute('//example.com/projects/example?agent=main', true), '/');
  assert.equal(notifyRoute('/settings', true), '/');
});

// PLAT-746: another monitor/app taking keyboard focus must not trigger Mac alerts.
test('Mac notifications stay quiet for a visible unfocused window, not hidden or minimized windows', () => {
  const window = { isDestroyed: () => false, isVisible: () => true, isMinimized: () => false, isFocused: () => false };
  assert.equal(notificationWindowActive(window, 'darwin'), true);
  assert.equal(notificationWindowActive(window, 'linux'), false);
  assert.equal(notificationWindowActive({ ...window, isMinimized: () => true }, 'darwin'), false);
  assert.equal(notificationWindowActive({ ...window, isVisible: () => false }, 'darwin'), false);
  assert.equal(notificationWindowActive({ ...window, isDestroyed: () => true }, 'darwin'), false);
  assert.equal(notificationWindowActive(null, 'darwin'), false);
});
