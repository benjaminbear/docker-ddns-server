const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const source = fs.readFileSync('dyndns/static/js/certificates.js', 'utf8');
function fixture(replies, ajax = false) {
    const timers = new Map(); let id = 0, reloads = 0, refreshes = 0;
    const message = {textContent: ''};
    const item = {dataset: {certPending: '7'}, querySelector: () => message};
    vm.runInNewContext(source, {
        document: {querySelector: () => null, querySelectorAll: () => [item]},
        window: {location: {reload: () => reloads++}, ...(ajax ? {refreshLists: async () => refreshes++} : {})}, AbortController,
        setTimeout: (fn, delay) => {timers.set(++id, {fn, delay}); return id;},
        clearTimeout: id => timers.delete(id),
        fetch: async () => {const r = replies.shift(); if (r instanceof Error) throw r; return {ok:true, json:async () => r};}
    });
    return {message, refreshes: () => refreshes, reloads: () => reloads, async tick() {const [key, timer] = timers.entries().next().value; timers.delete(key); await timer.fn();}, timers};
}
for (const terminal of ['valid', 'failed']) {
    test(`polls until ${terminal} without intermediate reload`, async () => {
        const f = fixture([[{id:7,status:'pending'}], [{id:7,status:'renewing'}], [{id:7,status:terminal}]]);
        await f.tick(); assert.equal(f.reloads(),0); assert.equal(f.timers.size,1);
        await f.tick(); assert.equal(f.reloads(),0);
        await f.tick(); assert.equal(f.reloads(),1); assert.equal(f.timers.size,0);
    });
}
test('network/auth errors and missing jobs never cause blind reload', async () => {
    const f = fixture([new Error('redirect'), [], [{id:7,status:'failed'}]]);
    await f.tick(); assert.equal(f.reloads(),0); assert.match(f.message.textContent,/Reconnecting/);
    assert.equal([...f.timers.values()][0].delay,4000);
    await f.tick(); assert.equal(f.reloads(),0);
    await f.tick(); assert.equal(f.reloads(),1);
});

test('terminal certificate state refreshes the list without reloading the page', async () => {
    const f = fixture([[{id:7,status:'valid'}]], true);
    await f.tick();
    assert.equal(f.refreshes(), 1);
    assert.equal(f.reloads(), 0);
    assert.equal(f.timers.size, 0);
});
