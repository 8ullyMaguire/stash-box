-- Content-viewing terms, recorded (aggregator phase 5, slice 5i).
--
-- `trust.AccessRequest.TermsAccepted` has existed since the gate was written, and until now NOTHING
-- wrote it: there was no column, no query and no mutation, so gate condition 5 (ReasonTerms) could
-- never be satisfied by any member -- a refusal with no way out. The onboarding query
-- (contentAccessSetup) reports what stands between a member and the content, and a condition with no
-- record is precisely the thing it cannot report honestly, so the record comes first.
--
-- It lives in user_trust because that is where the other two member-side conditions already live
-- (content_viewing_opt_in and visible_tiers): all three are things the MEMBER decides, as opposed to
-- level and contribution, which are earned. One table for "state the gate reads about a viewer".
--
-- NULLABLE, and that is the record: NULL means never accepted, which is different from a timestamp of
-- the epoch and must stay distinguishable. The mutation keeps the FIRST acceptance --
-- COALESCE(content_terms_accepted_at, now()) -- so re-accepting after a terms revision cannot rewrite
-- history through this column; a revision needs its own field, because "when did you accept the
-- CURRENT terms" and "when did you first accept any terms" are different questions.
ALTER TABLE "user_trust"
    ADD COLUMN "content_terms_accepted_at" TIMESTAMPTZ;

COMMENT ON COLUMN "user_trust"."content_terms_accepted_at" IS
    'When the member first accepted the content-viewing terms (gate condition 5). NULL = never accepted. Kept on first acceptance.';
