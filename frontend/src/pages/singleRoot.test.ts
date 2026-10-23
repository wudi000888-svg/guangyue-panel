import { describe, expect, it } from 'vitest';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { parse } from 'vue/compiler-sfc';

// Routed pages render inside <Transition mode="out-in">. A page with several
// root nodes cannot run its leave transition, so the next page never mounts
// and the panel shows a blank screen until reload (this happened on /pairing).
const NODE_ELEMENT = 1, NODE_COMMENT = 3, NODE_TEXT = 2;

function rootElements(source: string): string[] {
  const { descriptor } = parse(source);
  const children = descriptor.template?.ast?.children ?? [];
  const roots: string[] = [];
  for (const node of children as any[]) {
    if (node.type === NODE_COMMENT || (node.type === NODE_TEXT && !node.content.trim())) continue;
    if (node.type !== NODE_ELEMENT) { roots.push(`#${node.type}`); continue; }
    const conditional = (node.props ?? []).find((p: any) => p.type === 7 && (p.name === 'else' || p.name === 'else-if'));
    if (!conditional) roots.push(node.tag);
  }
  return roots;
}

describe('routed pages', () => {
  const dir = join(__dirname);
  for (const file of readdirSync(dir).filter(f => f.endsWith('.vue'))) {
    it(`${file} has a single root element`, () => {
      const roots = rootElements(readFileSync(join(dir, file), 'utf8'));
      expect(roots.length, `${file} roots: ${roots.join(', ')}`).toBe(1);
      expect(['Teleport', 'template', 'slot']).not.toContain(roots[0]);
    });
  }
});
