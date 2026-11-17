"""Case registration for the account capability/backoff composition boundary."""


def provider_admission_cases(root, model_type, case):
    model = model_type(
        "capability-admission", root / "models" / "provider" / "CapabilityAdmission.tla",
        ("NestedCapabilityWrapper", "GuardNilObserverOnly", "SkipCapabilityCheck",
         "DropSourcePolicy", "ReleaseStreamHoldEarly"),
        ("TypeOK", "NoPanic", "ObserverOwnsGate", "CallsRequireHTTP",
         "CapabilityRequired", "SourcePolicyRespected"), (("MaxRequests", 2),),
    )
    def check(name, source, switches=(), violates=None, witness=None):
        return case("capability-admission-" + name, source, switches, violates, witness, model.name)

    cases = (
        check("current", "one tracked admission owns capability, gate, policy hold and recorder"),
        check("known-nil-gate", "historical f6e9a9a: gate-less capability wrapper observer panics",
              ("NestedCapabilityWrapper",), "NoPanic"),
        check("detached-observer", "historical f6e9a9a: observer captures the outer nil gate",
              ("NestedCapabilityWrapper",), "ObserverOwnsGate"),
        check("nil-guard-only", "seeded: skip nil-gate observer but keep premature outer recording",
              ("NestedCapabilityWrapper", "GuardNilObserverOnly"), "CallsRequireHTTP"),
        check("skip-capability", "seeded: omit the role capability check",
              ("SkipCapabilityCheck",), "CapabilityRequired"),
        check("drop-policy", "seeded: clone gate without retaining trackedModel.policyUse",
              ("DropSourcePolicy",), "SourcePolicyRespected"),
        check("early-stream-release", "seeded: release source hold when Stream returns headers",
              ("ReleaseStreamHoldEarly",), "SourcePolicyRespected"),
        *(check(name, "witness", witness=witness) for name, witness in (
            ("reach-wait", "NeverCooldownWait"),
            ("reach-cancel-wait", "NeverCancelledWait"),
            ("reach-success", "NeverSuccessfulCall"),
            ("reach-stream", "NeverLiveStream"),
            ("reach-denial", "NeverPermissionRefusal"),
            ("reach-frozen-source", "NeverSourceRefusal"),
            ("reach-concurrent", "NeverConcurrentRequests"),
            ("reach-source-switch", "NeverSwitchAfterCompletion"),
        )),
    )
    return model, cases
