const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(__dirname + '/landing.js', 'utf8');
function fixture(copyFailure = false) {
  const panels = new Map();
  const groups = [4, 3].map((count, group) => {
    const tabs = Array.from({length:count}, (_, i) => ({
      id:`tab-${group}-${i}`, attrs:{'aria-controls':`panel-${group}-${i}`}, events:{},
      getAttribute(key){return this.attrs[key]}, setAttribute(key,value){this.attrs[key]=value},
      addEventListener(key,fn){this.events[key]=fn}, focus(){this.focused=true}
    }));
    tabs.forEach(tab => panels.set(tab.attrs['aria-controls'], {setAttribute(){}}));
    const list = {hidden:true, querySelectorAll(){return tabs}};
    return {tabs,list,querySelector(){return list}};
  });
  let copied;
  const button = {events:{}, hidden:true, addEventListener(key,fn){this.events[key]=fn},
    closest(){return {querySelector(){return {textContent:'bodek --resume'}}}}};
  vm.runInNewContext(source, {document:{querySelectorAll(s){return s==='[data-tabset]'?groups:[button]},getElementById(id){return panels.get(id)}},
    navigator:{clipboard:{async writeText(value){if(copyFailure)throw Error('denied');copied=value}}},setTimeout(){}});
  return {groups,panels,button,copied:()=>copied};
}
test('theme and launch tabs select independently and support keyboard navigation', () => {
  const {groups,panels}=fixture();
  function selected(group,index){
    assert.equal(group.list.hidden,false);
    group.tabs.forEach((tab,i)=>{
      assert.equal(tab.attrs['aria-selected'],String(i===index));
      assert.equal(tab.tabIndex,i===index?0:-1);
      assert.equal(panels.get(tab.attrs['aria-controls']).hidden,i!==index);
    });
  }
  groups.forEach(group=>{
    selected(group,0);
    group.tabs.forEach((tab,i)=>{tab.events.click();selected(group,i)});
    const last=group.tabs.length-1;
    for(const [from,key,to] of [[last,'ArrowRight',0],[0,'ArrowLeft',last],[last,'Home',0],[0,'End',last]]){
      let prevented=false;
      group.tabs[from].events.keydown({key,preventDefault(){prevented=true}});
      selected(group,to);assert.ok(prevented);assert.ok(group.tabs[to].focused);
    }
  });
  groups[0].tabs[0].events.click();selected(groups[1],2);
});
test('copy uses the command content and reports clipboard denial honestly', async () => {
  const success=fixture();await success.button.events.click();
  assert.equal(success.copied(),'bodek --resume');assert.equal(success.button.textContent,'Copied');
  const failure=fixture(true);await failure.button.events.click();
  assert.equal(failure.button.textContent,'Select code to copy');
});
