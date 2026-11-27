-- tailgate's services are called apps, so they aren't mixed up with tailscale services
ALTER TABLE users RENAME COLUMN has_all_services TO has_all_apps;
ALTER TABLE user_services RENAME TO user_apps;
ALTER TABLE user_apps RENAME COLUMN service TO app;
ALTER TABLE oidc_clients RENAME COLUMN service TO app;
-- rules see the app as app instead of service. a saved rule that doesn't compile blocks every request
UPDATE rules SET condition = replace(condition, 'service.', 'app.'), effects = replace(effects, 'service.', 'app.');
