import { describe, expect, it } from "vitest";
import { candidateGridLayout } from "./CandidateGrid";

describe("Candidate grid columns", () => {
  it("fits as many reading-width cards as the sheet holds, four at most", () => {
    // The desktop sheet (about 50rem minus its padding) holds three cards.
    expect(candidateGridLayout(752, 16)).toMatchObject({ columns: 3, cardWidth: 240, scale: 1 });
    expect(candidateGridLayout(1400, 16)).toMatchObject({ columns: 4, cardWidth: 240 });
    expect(candidateGridLayout(500, 16)).toMatchObject({ columns: 2, scale: 1 });
    // Enlarged text needs wider cards, so fewer fit.
    expect(candidateGridLayout(752, 32)).toMatchObject({ columns: 1, scale: 1 });
    // A sparse card never stretches across the grid, and never outgrows it.
    expect(candidateGridLayout(996, 16).cardWidth).toBe(240);
    expect(candidateGridLayout(200, 16).cardWidth).toBe(200);
  });
  it("keeps two proportionally scaled cards per row on phones", () => {
    // 390px phone: 358px grid with the 8px mobile gap; the 240px card shrinks whole.
    const phone = candidateGridLayout(358, 16, 8, true);
    expect(phone).toMatchObject({ columns: 2, cardWidth: 175 });
    expect(phone.scale).toBeCloseTo(175 / 240);
    // 320px phone still pairs cards.
    const narrow = candidateGridLayout(288, 16, 8, true);
    expect(narrow).toMatchObject({ columns: 2, cardWidth: 140 });
    expect(narrow.scale).toBeCloseTo(140 / 240);
    // Enlarged text would be shrunk back below readability: one full-size column.
    expect(candidateGridLayout(288, 32, 16, true)).toMatchObject({ columns: 1, cardWidth: 288, scale: 1 });
    expect(candidateGridLayout(358, 24, 12, true)).toMatchObject({ columns: 1, scale: 1 });
    // Once two 13.5rem cards fit, cards stop scaling.
    expect(candidateGridLayout(440, 16, 8, true)).toMatchObject({ columns: 2, scale: 1 });
    // Off phones a single column stays full size (enlarged desktop text).
    expect(candidateGridLayout(358, 16, 8)).toMatchObject({ columns: 1, scale: 1 });
  });
});
