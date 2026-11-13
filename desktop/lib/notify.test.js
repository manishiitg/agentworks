'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { notifyRoute } = require('./notify');

// PLAT-746: a company-server notification must not discard its agent target.
test('notification click preserves exact Main and side chat without arbitrary routes', () => {
  for (const agent of ['main', 'fork-example', 'agent-123']) {
    const route = '/crew/projects/example?agent=' + agent;
    assert.equal(notifyRoute(route, true), route);
    assert.equal(notifyRoute(route, false), route);
  }
  assert.equal(notifyRoute('/crew/projects/example?agent=fork-example&view=terminal', true), '/crew/projects/example?agent=fork-example');
  assert.equal(notifyRoute('/crew/projects/example?agent=..%2Fsecret', true), '/crew/projects/example');
  assert.equal(notifyRoute('//example.com/crew/projects/example?agent=main', true), '/home');
  assert.equal(notifyRoute('/settings', true), '/home');
});
