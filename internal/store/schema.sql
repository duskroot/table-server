CREATE TABLE IF NOT EXISTS users (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  openid VARCHAR(64) NOT NULL,
  nickname VARCHAR(64) NOT NULL DEFAULT '',
  avatar VARCHAR(512) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  UNIQUE KEY uk_openid (openid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS rooms (
  id VARCHAR(32) PRIMARY KEY,
  code CHAR(6) NOT NULL,
  active_code CHAR(6) NULL,
  host_user_id BIGINT NOT NULL,
  game_code VARCHAR(32) NOT NULL,
  variant VARCHAR(32) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL,
  config_json JSON NOT NULL,
  max_seats INT NOT NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  UNIQUE KEY uk_active_code (active_code),
  KEY idx_host (host_user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS room_members (
  room_id VARCHAR(32) NOT NULL,
  seat INT NOT NULL,
  user_id BIGINT NOT NULL,
  nickname VARCHAR(64) NOT NULL DEFAULT '',
  ready TINYINT NOT NULL DEFAULT 0,
  away TINYINT NOT NULL DEFAULT 0,
  PRIMARY KEY (room_id, seat),
  UNIQUE KEY uk_room_user (room_id, user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS plays (
  id VARCHAR(32) PRIMARY KEY,
  room_id VARCHAR(32) NOT NULL,
  game_code VARCHAR(32) NOT NULL,
  variant VARCHAR(32) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL,
  snapshot_json JSON NOT NULL,
  cursor_seq BIGINT NOT NULL DEFAULT 0,
  deadline DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  KEY idx_room_status (room_id, status),
  KEY idx_deadline (status, deadline)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS play_events (
  play_id VARCHAR(32) NOT NULL,
  seq BIGINT NOT NULL,
  scope INT NOT NULL,
  event_type VARCHAR(32) NOT NULL,
  payload_json JSON NOT NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (play_id, seq)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS play_actions (
  play_id VARCHAR(32) NOT NULL,
  seat INT NOT NULL,
  client_seq BIGINT NOT NULL,
  action_type VARCHAR(32) NOT NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (play_id, seat, client_seq)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS settlements (
  id BIGINT PRIMARY KEY AUTO_INCREMENT,
  play_id VARCHAR(32) NOT NULL,
  room_id VARCHAR(32) NOT NULL,
  user_id BIGINT NOT NULL,
  seat INT NOT NULL,
  delta INT NOT NULL,
  detail_json JSON NOT NULL,
  created_at DATETIME(3) NOT NULL,
  KEY idx_user (user_id, id),
  KEY idx_play (play_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
