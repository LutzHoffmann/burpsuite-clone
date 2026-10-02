import { describe, expect, it } from 'vitest';
import { draftFromRequest, newRepeaterTab, requestFromDraft, tabLabel } from './repeaterTabs';

describe('Repeater tabs', () => {
  it('keeps editable header text while converting a draft to a request', () => {
    const request = { method: 'POST', url: 'https://example.test/path', headers: { 'X-Test': ['one', 'two'] }, body: 'hello' };
    const draft = draftFromRequest(request);
    expect(requestFromDraft(draft)).toEqual(request);
    expect(newRepeaterTab(2, request).result).toBeNull();
    expect(tabLabel(newRepeaterTab(2, request))).toBe('2: POST example.test');
  });

  it('does not treat an incomplete URL as a different tab', () => {
    expect(tabLabel(newRepeaterTab(3, { method: 'GET', url: 'https://', headers: {}, body: '' }))).toBe('3: GET New request');
  });
});
