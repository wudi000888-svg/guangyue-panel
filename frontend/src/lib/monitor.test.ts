import { describe, expect, it } from 'vitest';
import { ratePath } from './monitor';
describe('live traffic graph',()=>{
  it('breaks at missing samples and long gaps instead of inventing traffic',()=>{
    const points=[{at:0,upload_rate:10,download_rate:20},{at:5000,upload_rate:null,download_rate:0},{at:10000,upload_rate:0,download_rate:10},{at:30000,upload_rate:5,download_rate:5}];
    expect(ratePath(points,'upload_rate',20).match(/M/g)).toHaveLength(3);
    expect(ratePath(points,'download_rate',20).match(/L/g)).toHaveLength(2);
    expect(ratePath(points,'upload_rate',20)).not.toContain('NaN');
    expect(ratePath([],'upload_rate',0)).toBe('');
  });
});
