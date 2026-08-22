-- Seed: initial category tree (admin-editable; PRD §5.8.3)
INSERT INTO categories (id, parent_id, name, slug, icon, description, sort_order) VALUES
  ('a1000000-0000-4000-8000-000000000001', NULL, 'Food & Dining', 'food-dining', 'utensils', 'Restaurants, street food, cafés, and everything edible.', 10),
  ('a1000000-0000-4000-8000-000000000002', NULL, 'Beauty & Care', 'beauty-care', 'sparkles', 'Salons, barbers, spas, and personal care.', 20),
  ('a1000000-0000-4000-8000-000000000003', NULL, 'Events & Entertainment', 'events-entertainment', 'camera', 'Photobooths, studios, entertainment venues.', 30),
  ('a1000000-0000-4000-8000-000000000004', NULL, 'Shopping', 'shopping', 'shopping-bag', 'Fashion, crafts, and retail.', 40),
  ('a1000000-0000-4000-8000-000000000005', NULL, 'Services', 'services', 'wrench', 'Professional and household services.', 50),
  ('a1000000-0000-4000-8000-000000000006', NULL, 'Health & Wellness', 'health-wellness', 'heart-pulse', 'Fitness, clinics, and wellness.', 60),

  ('a1000000-0000-4000-8000-000000000011', 'a1000000-0000-4000-8000-000000000001', 'Café', 'cafe', 'coffee', 'Coffee, tea, and pastry.', 1),
  ('a1000000-0000-4000-8000-000000000012', 'a1000000-0000-4000-8000-000000000001', 'Restaurant', 'restaurant', 'chef-hat', 'Sit-down dining.', 2),
  ('a1000000-0000-4000-8000-000000000013', 'a1000000-0000-4000-8000-000000000001', 'Street Food', 'street-food', 'flame', 'Street vendors and food stalls.', 3),
  ('a1000000-0000-4000-8000-000000000014', 'a1000000-0000-4000-8000-000000000001', 'Bakery', 'bakery', 'croissant', 'Bakeries and patisseries.', 4),
  ('a1000000-0000-4000-8000-000000000015', 'a1000000-0000-4000-8000-000000000001', 'Bar & Nightlife', 'bar-nightlife', 'martini', 'Bars, pubs, and nightlife.', 5),

  ('a1000000-0000-4000-8000-000000000021', 'a1000000-0000-4000-8000-000000000002', 'Salon', 'salon', 'scissors', 'Hair and beauty salons.', 1),
  ('a1000000-0000-4000-8000-000000000022', 'a1000000-0000-4000-8000-000000000002', 'Barber', 'barber', 'razor', 'Barbershops.', 2),
  ('a1000000-0000-4000-8000-000000000023', 'a1000000-0000-4000-8000-000000000002', 'Nail Studio', 'nail-studio', 'hand', 'Nail care.', 3),
  ('a1000000-0000-4000-8000-000000000024', 'a1000000-0000-4000-8000-000000000002', 'Spa', 'spa', 'flower', 'Spas and massages.', 4),

  ('a1000000-0000-4000-8000-000000000031', 'a1000000-0000-4000-8000-000000000003', 'Photobooth', 'photobooth', 'camera', 'Photobooth rentals and photo services.', 1),
  ('a1000000-0000-4000-8000-000000000032', 'a1000000-0000-4000-8000-000000000003', 'Photography Studio', 'photography-studio', 'aperture', 'Photography studios.', 2),
  ('a1000000-0000-4000-8000-000000000033', 'a1000000-0000-4000-8000-000000000003', 'Live Music', 'live-music', 'music', 'Live music venues.', 3),
  ('a1000000-0000-4000-8000-000000000034', 'a1000000-0000-4000-8000-000000000003', 'Event Venue', 'event-venue', 'calendar', 'Venues for events and parties.', 4),

  ('a1000000-0000-4000-8000-000000000041', 'a1000000-0000-4000-8000-000000000004', 'Fashion', 'fashion', 'shirt', 'Clothing and fashion.', 1),
  ('a1000000-0000-4000-8000-000000000042', 'a1000000-0000-4000-8000-000000000004', 'Crafts & Hobby', 'crafts-hobby', 'palette', 'Handmade and hobby shops.', 2),
  ('a1000000-0000-4000-8000-000000000043', 'a1000000-0000-4000-8000-000000000004', 'Electronics', 'electronics', 'cpu', 'Electronics and gadgets.', 3),

  ('a1000000-0000-4000-8000-000000000051', 'a1000000-0000-4000-8000-000000000005', 'Auto & Repair', 'auto-repair', 'car', 'Vehicle repair and care.', 1),
  ('a1000000-0000-4000-8000-000000000052', 'a1000000-0000-4000-8000-000000000005', 'Home Services', 'home-services', 'home', 'Cleaning, plumbing, and home care.', 2),
  ('a1000000-0000-4000-8000-000000000053', 'a1000000-0000-4000-8000-000000000005', 'Printing & Design', 'printing-design', 'printer', 'Printing and design services.', 3),

  ('a1000000-0000-4000-8000-000000000061', 'a1000000-0000-4000-8000-000000000006', 'Gym & Fitness', 'gym-fitness', 'dumbbell', 'Gyms and fitness studios.', 1),
  ('a1000000-0000-4000-8000-000000000062', 'a1000000-0000-4000-8000-000000000006', 'Clinic', 'clinic', 'stethoscope', 'Clinics and medical services.', 2),
  ('a1000000-0000-4000-8000-000000000063', 'a1000000-0000-4000-8000-000000000006', 'Wellness', 'wellness', 'leaf', 'Yoga, meditation, and wellness.', 3);

-- Note: no schema_migrations row is inserted here; the runner handles that automatically.
