// A response shows one product per product group; the group's own surface — criteria, sort, every
// candidate — is a sheet over the conversation (ADR-0086). A suite that measures candidates opens the
// sheet the way a reader would, and closes it before it touches the conversation or the composer again.
async function openResults(page, targetId) {
  if (await page.locator('.curation-target-sheet').count()) return;
  const scope = targetId ? `[data-result-target="${targetId}"]` : '[data-result-target]';
  // The product itself opens its details; the list opens from the row's right edge or the card's list button.
  await page.locator(`${scope} .curation-result__list, ${scope} .curation-result__more`).first().click();
  await page.locator('.curation-target-sheet').waitFor();
}

// Escape closes the top layer only, so a product's details close before the sheet does.
async function closeResults(page) {
  for (let layers = 0; layers < 3 && await page.locator('.curation-target-sheet').count(); layers++) {
    await page.keyboard.press('Escape');
    await page.waitForTimeout(250);
  }
}

module.exports = { openResults, closeResults };
