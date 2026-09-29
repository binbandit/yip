-- The built-in scripted provider ("fake") was removed. Forget the accounts
-- and installations it reported so they don't linger in the accounts list.
-- Engineers still set to it keep that setting until the owner picks a real
-- provider; their work waits with a reason saying so.
DELETE FROM provider_installations WHERE provider = 'fake';
DELETE FROM provider_profiles WHERE provider = 'fake';
