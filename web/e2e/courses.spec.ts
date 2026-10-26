import { expect, test } from "@playwright/test";

// M2 journey on the fake lab: the imported demo course from the catalogue to its course page and through the
// player. The page signs itself in (no cookie at the start).

test("from the catalogue through a course in the player", async ({ page }) => {
  await page.goto("/courses");
  await page.getByRole("link", { name: "Kubernetes introduction" }).click();
  await expect(page).toHaveURL("/courses/k8s-intro");
  await expect(page.getByRole("heading", { level: 1, name: "Kubernetes introduction" })).toBeVisible();

  await page.getByRole("link", { name: "Start course" }).click();
  await expect(page).toHaveURL("/learn/k8s-intro/what-is-a-pod");
  await expect(page.getByRole("heading", { level: 1, name: "What is a pod" })).toBeVisible();
  await expect(page.getByRole("complementary", { name: "Note" })).toBeVisible();

  await page.getByRole("link", { name: /^Next: Quiz 1\.2/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Pods check" })).toBeVisible();
  await page.keyboard.press("ArrowRight");
  await expect(page).toHaveURL("/learn/k8s-intro/meet-the-cluster");
  await expect(page.getByRole("heading", { name: /List the nodes/ })).toBeVisible();
});

test("a lecture's code is highlighted and copied", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/learn/k8s-intro/meet-the-cluster");
  const block = page.locator(".code-shiki").first();
  await expect(block).toBeVisible(); // Shiki loaded and coloured it
  await page.getByRole("button", { name: "Copy code" }).first().click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("opsforge kubectl get nodes");
});

test("the course content is a drawer on a phone and a sidebar on a desktop", async ({ page }) => {
  await page.goto("/learn/k8s-intro/pods-check");
  const toggle = page.getByRole("button", { name: "Course content" });
  const content = page.getByRole("navigation", { name: "Course content" });
  const narrow = (page.viewportSize()?.width ?? 1280) < 900;
  if (narrow) {
    await expect(content).toBeHidden();
    await toggle.click();
  }
  await expect(content.getByRole("link", { name: /1\.2 Pods check/ })).toHaveAttribute("aria-current", "page");
  await content.getByRole("link", { name: /1\.4 First pod/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "First pod" })).toBeVisible();
});

test("a quiz is drawn, scored and passed on the server", async ({ page }) => {
  await page.goto("/learn/k8s-intro/pods-check");
  await page.getByRole("button", { name: /^(Start quiz|Start over)$/ }).click();
  // The server shuffles questions and choices: answer each by what it asks
  for (const n of [1, 2]) {
    await expect(page.getByText(`Question ${String(n)} of 2`)).toBeVisible();
    if (await page.getByText("What do the containers of one pod share?").isVisible())
      await page.getByRole("radio", { name: "A network namespace" }).click();
    else
      for (const phase of ["Pending", "Running", "Succeeded"])
        await page.getByRole("checkbox", { name: phase }).click();
    if (n === 1) await page.getByRole("button", { name: "Next" }).click();
  }
  await page.getByRole("button", { name: "Submit quiz" }).click();
  await expect(page.getByRole("heading", { name: "Passed: 2 of 2" })).toBeVisible();
  await expect(page.getByLabel("Right")).toHaveCount(2);
});

test("a lab runs in its sandbox: terminal, Run, checks, then the pass", async ({ page }) => {
  await page.goto("/learn/k8s-intro/meet-the-cluster");
  await page.getByRole("button", { name: "Start lab" }).click();
  // The fake OpsForge's shell: a banner, then it echoes what is typed
  const terminal = page.locator(".xterm-rows");
  await expect(terminal).toContainText("Lab terminal in", { timeout: 15_000 });
  await expect(page.getByText("Connected")).toBeVisible();

  await page.getByRole("button", { name: "Run in the terminal" }).first().click();
  await expect(terminal).toContainText("opsforge kubectl get nodes");

  await page.getByRole("button", { name: "Check" }).click();
  await expect(page.getByText("Step 2 of 2")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "Applied" }).click();
  await expect(page.getByRole("heading", { name: "Lab passed" })).toBeVisible({ timeout: 15_000 });
});
