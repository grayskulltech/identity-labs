// CastleOps policy set, v1.
//
// Every request is a Cedar authorization query:
//   principal  User::"<subject>"            parents: Group::"<group>" ...   attrs: subject
//   action     Action::"<tool id>"          parents: Action::"group:<kind>", Action::"group:<domain>", Action::"group:<id prefixes>"
//   resource   Tool::"<tool id>"            attrs: toolId, kind, domain, version
//   context    see pdp.go buildContext()
//
// Time-derived and version-derived facts are computed by the gateway and
// passed as plain longs and booleans (attestationAgeSeconds, hour,
// attestedOSAtLeastMinimum, ...). Policies state rules; they do not parse
// dates or version strings. Every attribute a policy references is always
// present in context, so a missing-attribute error cannot occur and a forbid
// can never be skipped by an evaluation error.
//
// Semantics reminder: any forbid that matches wins over every permit.

// Posture floor. Applies to every principal, every action, every tool.
@id("forbid.posture")
forbid (principal, action, resource)
unless {
  context.device.status == "active" &&
  context.device.enrolled &&
  context.device.modelAllowed &&
  context.device.attestedOSAtLeastMinimum &&
  context.device.reportedOSAtLeastMinimum &&
  context.device.attestationAgeSeconds <= context.policy.attestationMaxAgeSeconds &&
  context.device.checkinAgeSeconds <= context.policy.checkinMaxAgeSeconds
};

// A write is signed by the biometry-gated presence key or it does not happen.
@id("forbid.write_requires_presence")
forbid (principal, action, resource)
when { resource.kind == "write" && !context.presenceVerified };

// An elevate tool executes only under a single-use elevation bound to this
// exact tool and these exact arguments, with a human present.
@id("forbid.elevate_requires_matching_elevation")
forbid (principal, action, resource)
when { resource.kind == "elevate" }
unless {
  context.elevation.present &&
  context.elevation.tool == resource.toolId &&
  context.elevation.argsHash == context.argsHash &&
  context.presenceVerified
};

// Nobody approves their own elevation. Two people, always.
@id("forbid.elevation_self_approval")
forbid (principal, action, resource)
when {
  resource.kind == "elevate" &&
  context.elevation.present &&
  context.elevation.approver == principal.subject
};

// Minors act between 07:00 and 21:00 household time.
@id("forbid.minors_hours")
forbid (principal in Group::"minors", action, resource)
unless { context.hour >= 7 && context.hour < 21 };

// Reads.
@id("permit.family_calendar_read")
permit (
  principal in Group::"family",
  action == Action::"calendar.events.read",
  resource == Tool::"calendar.events.read"
)
when {
  context.args has calendar &&
  (context.args.calendar != "adults" || principal in Group::"adults")
};

// Writes. Duration and VLAN are bounded here, not in a scope string.
@id("permit.family_guest_provision")
permit (
  principal in Group::"family",
  action == Action::"network.guest.provision",
  resource == Tool::"network.guest.provision"
)
when {
  context.args has duration_minutes && context.args.duration_minutes <= 240 &&
  context.args has vlan && context.args.vlan == "guest"
};

// Elevations. Adults may reach the firewall tool; the forbid above supplies
// the elevation requirement. Minors have no permit here at all.
@id("permit.adults_firewall_with_elevation")
permit (
  principal in Group::"adults",
  action == Action::"network.firewall.rule.write",
  resource == Tool::"network.firewall.rule.write"
);
