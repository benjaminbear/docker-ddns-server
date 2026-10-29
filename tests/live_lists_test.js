const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync('dyndns/static/js/live-lists.js', 'utf8');
const core = source.slice(source.indexOf(' function value('), source.indexOf(' function controls('));
function fixture() {
    const rows = Array.from({length:5000}, (_, i) => ({dataset:{name:`device-${i}`,ttl:String(i)},hidden:false}));
    const state = {cards:true,fields:['name','ttl'],rows,filters:['',''],sort:null,direction:1,page:0,size:25,
        numeric:new Set([1]),addressColumns:new Set(),sortButtons:[],
        body:{children:[],replaceChildren(...nodes){this.children=nodes;}},
        summary:{},prev:{},next:{},empty:{},controls:{offsetHeight:40},root:{style:{setProperty(){}}}};
    const context = vm.createContext({collator:new Intl.Collator(undefined,{numeric:true}),searchCache:new WeakMap()});
    vm.runInContext(core,context);
    return {state,apply:()=>context.apply(state)};
}
test('large lists attach only the selected page and retain all entries for filtering', () => {
    const {state,apply} = fixture();
    apply();
    assert.equal(state.rows.length,5000);
    assert.equal(state.body.children.length,25);
    assert.equal(state.summary.textContent,'1–25 of 5000');
    state.filters[0]='device-49'; apply();
    assert.equal(state.summary.textContent,'1–25 of 111');
    state.page=4; apply();
    assert.equal(state.body.children.length,11);
    assert.equal(state.summary.textContent,'101–111 of 111');
    assert.equal(state.next.disabled,true);
});
test('sorting and resetting work across detached pages, with empty matches handled', () => {
    const {state,apply} = fixture();
    state.sort=1;state.direction=-1;apply();
    assert.equal(state.body.children[0].dataset.ttl,'4999');
    state.page=1;apply();
    assert.equal(state.body.children[0].dataset.ttl,'4974');
    state.sort=null;state.page=0;apply();
    assert.equal(state.body.children[0].dataset.ttl,'0');
    state.filters[0]='no-match';apply();
    assert.equal(state.body.children.length,0);
    assert.equal(state.summary.textContent,'0–0 of 0');
    assert.equal(state.empty.hidden,false);
});
