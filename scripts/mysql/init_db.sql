-- MySQL dump 10.13  Distrib 8.0.42, for Linux (x86_64)
--
-- Host: 127.0.0.1    Database: clouddisk_db
-- ------------------------------------------------------
-- Server version	8.0.42-0ubuntu0.24.04.1

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;
/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;
/*!50503 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;

--
-- Table structure for table `files`
--

DROP TABLE IF EXISTS `files`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `files` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `uuid` varchar(36) NOT NULL,
  `user_id` bigint unsigned NOT NULL,
  `parent_folder_id` bigint unsigned DEFAULT NULL,
  `file_name` varchar(255) NOT NULL,
  `is_folder` tinyint unsigned NOT NULL DEFAULT '0',
  `size` bigint unsigned NOT NULL DEFAULT '0',
  `mime_type` varchar(128) DEFAULT NULL,
  `oss_bucket` varchar(64) DEFAULT NULL,
  `oss_key` varchar(255) DEFAULT NULL,
  `md5_hash` varchar(32) DEFAULT NULL,
  `status` tinyint unsigned NOT NULL DEFAULT '1',
  `deleted_at` datetime(3) DEFAULT NULL,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uni_files_uuid` (`uuid`),
  KEY `fk_files_user` (`user_id`),
  KEY `fk_files_parent_folder` (`parent_folder_id`),
  KEY `idx_files_deleted_at` (`deleted_at`),
  CONSTRAINT `fk_files_parent_folder` FOREIGN KEY (`parent_folder_id`) REFERENCES `files` (`id`),
  CONSTRAINT `fk_files_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=19 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `files`
--

/*!40000 ALTER TABLE `files` DISABLE KEYS */;
INSERT INTO `files` VALUES (8,'2eae415a-5735-43fc-9953-c02f11091ce7',4,NULL,'networks',0,91,'application/octet-stream','go-clouddisk-bucket','7028a4bd02c1d276aa45f0973b85fe1b','7028a4bd02c1d276aa45f0973b85fe1b',1,NULL,'2025-07-15 16:35:39.911','2025-07-15 16:35:39.911'),(16,'e749f838-c8b5-4cb2-9cb8-58be011628eb',4,NULL,'newtest1',1,0,NULL,NULL,NULL,NULL,1,NULL,'2025-07-16 21:42:56.262','2025-07-17 18:15:13.383'),(17,'87b6ffd3-b36e-4d6d-b531-9e8df70667ed',4,16,'a(1).txt',0,12,'text/plain','go-clouddisk-bucket','757228086dc1e621e37bed30e0b73e17.txt','757228086dc1e621e37bed30e0b73e17',1,NULL,'2025-07-16 21:43:50.003','2025-07-16 22:01:15.246'),(18,'e5117149-6c3e-4497-9419-12a3c9194434',4,16,'a.txt',0,12,'text/plain','go-clouddisk-bucket','757228086dc1e621e37bed30e0b73e17.txt','757228086dc1e621e37bed30e0b73e17',1,NULL,'2025-07-16 21:44:11.014','2025-07-16 21:44:11.014');
/*!40000 ALTER TABLE `files` ENABLE KEYS */;

--
-- Table structure for table `users`
--

DROP TABLE IF EXISTS `users`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `users` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '用户唯一ID',
  `username` varchar(64) COLLATE utf8mb4_unicode_ci NOT NULL,
  `password_hash` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL,
  `email` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL,
  `total_space` bigint unsigned NOT NULL DEFAULT '0',
  `used_space` bigint unsigned NOT NULL DEFAULT '0',
  `status` tinyint unsigned NOT NULL DEFAULT '1',
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uni_users_username` (`username`),
  UNIQUE KEY `uni_users_email` (`email`)
) ENGINE=InnoDB AUTO_INCREMENT=6 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户表';
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `users`
--

/*!40000 ALTER TABLE `users` DISABLE KEYS */;
INSERT INTO `users` VALUES (1,'user1','$2a$10$abcdefghijklmnopqrstuvwxyzabcdefghi','user1@example.com',1073741824,0,1,'2025-07-11 14:35:23.000','2025-07-11 14:35:23.000'),(2,'user2','$2a$10$abcdefghijklmnopqrstuvwxyzabcdefghj','user2@example.com',5368709120,0,1,'2025-07-11 14:35:23.000','2025-07-11 14:35:23.000'),(3,'newuser','$2a$10$PUTrtC22reQqekFh3k59judwWFkpFMjwgMVuVFxDLObZOs0xK0pe6','new@example.com',1073741824,0,1,'2025-07-12 23:40:03.943','2025-07-12 23:40:03.943'),(4,'testuser','$2a$10$N.EXBCJgP89t.5M0OJc0uecL1ICKTxx4ASiN5.qHd9XJLaB5NZbSO','test@example.com',1073741824,0,1,'2025-07-13 21:41:38.904','2025-07-13 21:41:38.904'),(5,'eecho','$2a$10$gH.PBwukGRzo2E.EmGDQW.HpeCuT1pZ.sHKV.ThWhVUpvPzIW2BLq','wc@example.com',1073741824,0,1,'2025-07-14 14:53:28.103','2025-07-14 14:53:28.103');
/*!40000 ALTER TABLE `users` ENABLE KEYS */;

--
-- Dumping routines for database 'clouddisk_db'
--
/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;

/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;
/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;
/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;

-- Dump completed on 2025-07-17 21:20:45
