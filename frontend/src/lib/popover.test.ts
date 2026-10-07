import {describe,expect,it} from 'vitest';
import {placePopover} from './popover';

describe('updater viewport placement',()=>{
  it('moves an expanded bottom-anchored panel above the viewport edge',()=>{
    const viewport={left:0,top:0,width:1280,height:720},anchor={left:160,bottom:680};
    const compact=placePopover(viewport,anchor,220,false),expanded=placePopover(viewport,anchor,580,false);
    expect(compact.top+220).toBeLessThanOrEqual(708);
    expect(expanded.top).toBeLessThan(compact.top);
    expect(expanded.top+580).toBeLessThanOrEqual(708);
  });
  it('keeps long content scrollable within a short mobile visual viewport',()=>{
    const viewport={left:15,top:120,width:320,height:260};
    const result=placePopover(viewport,{left:-300,bottom:760},900,true);
    expect(result.left).toBeGreaterThanOrEqual(27);
    expect(result.left+result.width).toBeLessThanOrEqual(323);
    expect(result.top).toBe(132);
    expect(result.top+result.maxHeight).toBe(368);
  });
});
