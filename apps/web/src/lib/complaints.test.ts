import { describe, expect, it } from 'vitest';
import { daysLeft } from './complaints';

describe('daysLeft', () => {
  // Deadline: end of Friday 13 Nov 2026 in Lima (Saturday 04:59:59 UTC).
  const due = '2026-11-14T04:59:59.999999Z';

  it('counts calendar days in Lima', () => {
    expect(daysLeft(due, new Date('2026-11-10T15:00:00Z'))).toBe(3);
    // 23:30 on Thursday in Lima is still the day before the deadline's day.
    expect(daysLeft(due, new Date('2026-11-13T04:30:00Z'))).toBe(1);
  });

  it('is 0 on the last day and negative once it passed', () => {
    expect(daysLeft(due, new Date('2026-11-13T23:00:00Z'))).toBe(0);
    expect(daysLeft(due, new Date('2026-11-14T05:00:00Z'))).toBe(-1);
    expect(daysLeft(due, new Date('2026-11-17T15:00:00Z'))).toBe(-4);
  });
});
