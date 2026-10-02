// @vitest-environment node
import { describe, expect, it } from "vitest";
import { resourceRelationRoute, type Bounds } from "./routing";

const card = (x: number, y: number): Bounds => ({ x, y, width: 220, height: 120 });

describe("resourceRelationRoute", () => {
  it("balances horizontal bends between the facing edges of different-sized cards", () => {
    const source = { ...card(40, 160), height: 168 };
    const target = card(420, 40);
    const route = resourceRelationRoute(source, target);
    expect(route.sourceSide).toBe("right");
    expect(route.targetSide).toBe("left");
    expect(route.vertices).toEqual([
      { x: 340, y: 244 },
      { x: 340, y: 100 },
    ]);
    expect(route.labelPosition.distance).toBeCloseTo(0.5);
  });

  it("uses a direct line when the facing anchors are aligned", () => {
    const route = resourceRelationRoute(card(0, 0), card(400, 0));
    expect(route.vertices).toEqual([]);
    expect(route.labelPosition.distance).toBe(0.5);
  });

  it("balances vertical bends and points upwards in the reversed layout", () => {
    const route = resourceRelationRoute(card(120, 420), card(0, 0));
    expect(route.sourceSide).toBe("top");
    expect(route.targetSide).toBe("bottom");
    expect(route.vertices).toEqual([
      { x: 230, y: 270 },
      { x: 110, y: 270 },
    ]);
  });

  it("reverses a horizontal route without doubling back into a card", () => {
    const forward = resourceRelationRoute(card(0, 0), card(400, 120));
    const reverse = resourceRelationRoute(card(400, 120), card(0, 0));
    expect(reverse.sourceSide).toBe("left");
    expect(reverse.targetSide).toBe("right");
    expect(reverse.vertices).toEqual([...forward.vertices!].reverse());
  });

  it("separates reciprocal relations at the anchors and middle lanes", () => {
    const forward = resourceRelationRoute(card(0, 0), card(400, 120), [], true);
    const reverse = resourceRelationRoute(card(400, 120), card(0, 0), [], true);
    expect(forward.sourceOffset).not.toEqual(reverse.targetOffset);
    expect(forward.vertices![0]!.x).not.toBe(reverse.vertices![0]!.x);
  });

  it("falls back to obstacle routing if another card blocks any leg", () => {
    const route = resourceRelationRoute(card(0, 0), card(800, 200), [card(400, 80)]);
    expect(route.vertices).toBeUndefined();
    expect(route.sourceSide).toBe("right");
    expect(route.targetSide).toBe("left");
  });

  it("routes overlapping endpoints through their outer sides and an outside label lane", () => {
    const route = resourceRelationRoute(card(0, 0), card(180, 40));
    expect(route.sourceSide).toBe("left");
    expect(route.targetSide).toBe("right");
    expect(route.vertices![1]!.y).toBeLessThan(-40);
    expect(route.vertices![2]!.y).toBeLessThan(-40);
  });

  it("puts the label on a straight segment when the middle leg is short", () => {
    const route = resourceRelationRoute(card(0, 0), card(600, 20));
    // Half of the path lands on the short vertical leg between two corners.
    expect(route.labelPosition.distance).toBeLessThan(0.4);
  });

  it("wraps labels inside a narrow gap while keeping space for body padding", () => {
    const route = resourceRelationRoute(card(0, 160), card(310, 0));
    expect(route.labelMaxWidth + 20).toBeLessThan(90);
    expect(route.labelMaxWidth).toBeGreaterThan(40);
  });

  it.each([10, 40, 59])(
    "moves the label above cards when the horizontal gap is only %ipx",
    (gap) => {
      const route = resourceRelationRoute(card(0, 40), card(220 + gap, 0));
      expect(route.sourceSide).toBe("top");
      expect(route.targetSide).toBe("top");
      expect(route.vertices![0]!.y + 30).toBeLessThan(0);
      expect(route.vertices![1]!.y).toBe(route.vertices![0]!.y);
      expect(route.labelMaxWidth).toBe(160);
    },
  );

  it("keeps a padded label clear of vertically adjacent cards", () => {
    const route = resourceRelationRoute(card(0, 0), card(20, 130));
    expect(route.sourceSide).toBe("right");
    expect(route.targetSide).toBe("right");
    expect(route.vertices![0]!.x - route.labelMaxWidth / 2 - 10).toBeGreaterThan(240);
  });

  it("chooses the lower outside lane when another card blocks the upper lane", () => {
    const route = resourceRelationRoute(card(0, 40), card(250, 0), [card(125, -160)]);
    expect(route.sourceSide).toBe("bottom");
    expect(route.targetSide).toBe("bottom");
    expect(route.vertices![0]!.y).toBeGreaterThan(160 + 12);
  });

  it("clears the complete multiline label when the upper centerline itself is unblocked", () => {
    const route = resourceRelationRoute(card(0, 40), card(250, 0), [card(125, -200)]);
    expect(route.sourceSide).toBe("bottom");
    expect(route.targetSide).toBe("bottom");
    expect(route.vertices![0]!.y).toBeGreaterThan(160 + 30);
  });

  it("clears the label width beside a vertical outside lane", () => {
    const route = resourceRelationRoute(card(0, 0), card(20, 130), [card(370, 80)]);
    expect(route.sourceSide).toBe("left");
    expect(route.targetSide).toBe("left");
    expect(route.vertices![0]!.x + (route.labelMaxWidth + 20) / 2).toBeLessThan(-4);
  });

  it("uses obstacle routing through a safe label waypoint when both outside lanes are blocked", () => {
    const source = card(0, 40),
      target = card(250, 0);
    const route = resourceRelationRoute(source, target, [card(125, -160), card(125, 190)]);
    expect(route.vertices).toBeUndefined();
    expect(route.waypoints).toHaveLength(1);
    const waypoint = route.waypoints![0]!;
    expect(waypoint.y + 30).toBeLessThan(-160 - 12);
    expect(route.labelPosition).toEqual({
      distance: 0,
      offset: { x: waypoint.x - 110, y: waypoint.y - 40 },
    });
  });
});
