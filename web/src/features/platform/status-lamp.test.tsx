import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { fixtures, http, HttpResponse, server } from "@/test/server";
import { renderWithQuery } from "@/test/render";

import { StatusLamp } from "./status-lamp";

describe("StatusLamp", () => {
  it("is dark while the first check runs", () => {
    renderWithQuery(<StatusLamp />);
    expect(screen.getByRole("status")).toHaveTextContent("Checking the platform");
    expect(screen.getByRole("status")).toHaveAttribute("data-lamp", "off");
  });

  it("shows green when every check passed", async () => {
    renderWithQuery(<StatusLamp />);
    expect(await screen.findByText("Platform ready")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveAttribute("data-lamp", "green");
  });

  it("names the failed check and its reason on a 503", async () => {
    server.use(http.get("*/readyz", () => HttpResponse.json(fixtures.databaseDown, { status: 503 })));
    renderWithQuery(<StatusLamp />);
    expect(await screen.findByText("database unavailable")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveAttribute("data-lamp", "red");
    expect(screen.getByRole("status")).toHaveAttribute("title", "database: connection refused");
  });

  it("shows amber when only OpsForge is out of reach: the courses work, labs do not", async () => {
    server.use(http.get("*/readyz", () => HttpResponse.json(fixtures.labsOffline)));
    renderWithQuery(<StatusLamp />);
    expect(await screen.findByText("Labs offline")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveAttribute("data-lamp", "amber");
    expect(screen.getByRole("status")).toHaveAttribute(
      "title",
      "Labs need OpsForge. opsforge: opsforge speaks contract 2, academy speaks 1",
    );
  });

  it("says when the platform does not answer", async () => {
    server.use(http.get("*/readyz", () => HttpResponse.error()));
    renderWithQuery(<StatusLamp />);
    expect(await screen.findByText("Platform unreachable")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveAttribute("data-lamp", "red");
  });
});
