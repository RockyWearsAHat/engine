import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

function getRepoRoot(): string {
  const testFileDir = path.dirname(fileURLToPath(import.meta.url));
  return path.resolve(testFileDir, '../../../..');
}

function readWorkingBehaviorHeadings(markdown: string): string[] {
  const matches = markdown.match(/^##\s+(.+)$/gm) ?? [];
  return matches
    .map((line) => line.replace(/^##\s+/, '').trim())
    .filter((heading) => !heading.includes('(IN PROGRESS)'));
}

describe('WORKING_BEHAVIORS contract', () => {
  it('EachShippedBehaviorSection_HasConcreteTestLinks', () => {
    const root = getRepoRoot();
    const workingBehaviorsPath = path.join(root, '.github', 'WORKING_BEHAVIORS.md');
    const mapPath = path.join(root, '.github', 'working-behaviors-test-map.json');

    const workingBehaviors = fs.readFileSync(workingBehaviorsPath, 'utf8');
    const headings = readWorkingBehaviorHeadings(workingBehaviors);
    const map = JSON.parse(fs.readFileSync(mapPath, 'utf8')) as Record<string, string[]>;

    const mappedHeadings = Object.keys(map);

    expect(new Set(mappedHeadings)).toEqual(new Set(headings));

    for (const heading of headings) {
      const linkedTests = map[heading];
      expect(Array.isArray(linkedTests)).toBe(true);
      expect(linkedTests.length).toBeGreaterThan(0);

      for (const testFile of linkedTests) {
        const absolutePath = path.join(root, testFile);
        expect(fs.existsSync(absolutePath)).toBe(true);
      }
    }
  });
});
